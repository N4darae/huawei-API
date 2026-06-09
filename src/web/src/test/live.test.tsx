import { describe, expect, it } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { ApiFailure, apiJson } from '../api/client'
import { LiveProvider, dispatchEvent, useLive } from '../api/sse'
import type { NoticePayload } from '../api/sse'
import { qk } from '../api/keys'
import type { Proxy } from '../api/keys'
import { testQueryClient } from './render'
import { db, freshDb } from './state'

type Listener = (e: MessageEvent<string>) => void

class FakeEventSource {
  static last: FakeEventSource | null = null
  readonly listeners = new Map<string, Listener[]>()
  onerror: ((e: Event) => void) | null = null
  closed = false

  constructor(public readonly url: string) {
    FakeEventSource.last = this
  }

  addEventListener(type: string, fn: Listener): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }

  removeEventListener(type: string, fn: Listener): void {
    this.listeners.set(type, (this.listeners.get(type) ?? []).filter((x) => x !== fn))
  }

  close(): void {
    this.closed = true
  }

  emit(type: string, envelope: unknown): void {
    for (const fn of this.listeners.get(type) ?? []) {
      fn({ data: JSON.stringify(envelope) } as MessageEvent<string>)
    }
  }

  fail(): void {
    this.onerror?.(new Event('error'))
  }
}

function Probe() {
  const live = useLive()
  return (
    <span>
      status:{live.status} node:{live.nodeId ?? 'none'} reconnects:{live.reconnects}
    </span>
  )
}

function withFakeEventSource<T>(fn: () => T): T {
  const holder = globalThis as { EventSource?: unknown }
  const previous = holder.EventSource
  holder.EventSource = FakeEventSource
  try {
    return fn()
  } finally {
    holder.EventSource = previous
  }
}

describe('event stream state is reported honestly', () => {
  it('says there is no stream when the runtime has no EventSource', async () => {
    const client = testQueryClient()
    render(
      <QueryClientProvider client={client}>
        <LiveProvider topics={['proxies']}>
          <Probe />
        </LiveProvider>
      </QueryClientProvider>,
    )
    expect(await screen.findByText(/status:unsupported/)).toBeTruthy()
  })

  it('only claims to be live once hello arrives, and reports a lost stream', async () => {
    await withFakeEventSource(async () => {
      const client = testQueryClient()
      render(
        <QueryClientProvider client={client}>
          <LiveProvider topics={['proxies', 'operations']}>
            <Probe />
          </LiveProvider>
        </QueryClientProvider>,
      )

      expect(await screen.findByText(/status:connecting/)).toBeTruthy()
      const src = FakeEventSource.last
      expect(src?.url).toContain('topics=proxies%2Coperations')

      act(() => {
        src?.emit('hello', {
          type: 'hello',
          topic: 'system',
          node_id: 'node-a',
          subject: '',
          ts: Date.now(),
          data: { node_id: 'node-a', server_time: Date.now(), topics: ['proxies'], product: 'dongled' },
        })
      })
      await waitFor(() => expect(screen.getByText(/status:live/)).toBeTruthy())
      expect(screen.getByText(/node:node-a/)).toBeTruthy()

      act(() => src?.fail())
      await waitFor(() => expect(screen.getByText(/status:reconnecting/)).toBeTruthy())
    })
  })
})

describe('event dispatch', () => {
  it('merges a proxy patch into the cached list and detail', () => {
    const client = testQueryClient()
    const seed = freshDb().proxies
    client.setQueryData(qk.proxies({}), { items: seed, total: seed.length })
    client.setQueryData(qk.proxy('px01'), { proxy: seed[0] as Proxy })

    dispatchEvent(client, {
      type: 'proxy.patch',
      topic: 'proxies',
      node_id: 'node-a',
      subject: 'px01',
      ts: Date.now(),
      data: { id: 'px01', fields: { wan_ip: '100.71.99.99', state: 'degraded' } },
    })

    const list = client.getQueryData<{ items: Proxy[] }>(qk.proxies({}))
    expect(list?.items[0]?.wan_ip).toBe('100.71.99.99')
    expect(list?.items[0]?.state).toBe('degraded')
    expect(client.getQueryData<{ proxy: Proxy }>(qk.proxy('px01'))?.proxy.wan_ip).toBe('100.71.99.99')
  })

  it('stores an operation update under its own key', () => {
    const client = testQueryClient()
    dispatchEvent(client, {
      type: 'op.update',
      topic: 'operations',
      node_id: 'node-a',
      subject: 'op-1',
      ts: Date.now(),
      data: {
        id: 'op-1',
        kind: 'rotate',
        subject_type: 'proxy',
        subject_id: 'px01',
        state: 'running',
        step: 'hold',
        pct: 40,
        started_at: db.now,
        deadline_at: db.now + 90_000,
        finished_at: null,
        trigger: 'admin_ui',
      },
    })
    expect(client.getQueryData<{ step: string }>(qk.operation('op-1'))?.step).toBe('hold')
  })

  it('hands a system notice to the toast layer', () => {
    const client = testQueryClient()
    const seen: NoticePayload[] = []
    dispatchEvent(
      client,
      {
        type: 'system.notice',
        topic: 'system',
        node_id: 'node-a',
        subject: '',
        ts: Date.now(),
        data: { level: 'error', title: 'egress fence broken', detail: 'slot 2' },
      },
      (n) => seen.push(n),
    )
    expect(seen).toEqual([{ level: 'error', title: 'egress fence broken', detail: 'slot 2' }])
  })
})

describe('api error mapping', () => {
  it('turns 409 op_in_progress into an attachable failure, not a generic error', async () => {
    db.rotateResponse = { kind: 'conflict', opId: 'op-existing' }
    const err = await apiJson('POST', '/api/v1/proxies/px01/rotate').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiFailure)
    const failure = err as ApiFailure
    expect(failure.status).toBe(409)
    expect(failure.isOpInProgress).toBe(true)
    expect(failure.operationId).toBe('op-existing')
  })

  it('reads Retry-After off a 429', async () => {
    db.rotateResponse = { kind: 'rate_limited', retryAfter: 42 }
    const err = await apiJson('POST', '/api/v1/proxies/px01/rotate').catch((e: unknown) => e)
    const failure = err as ApiFailure
    expect(failure.isRateLimited).toBe(true)
    expect(failure.retryAfterS).toBe(42)
  })
})
