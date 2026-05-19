import { createContext, createElement, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { QueryClient } from '@tanstack/react-query'
import { EVENT_TYPES, isEventType } from './events'
import type { AnyEvent, EventEnvelope, Topic } from './events'
import { EVENTS_PATH, qk } from './keys'
import type { Dongle, Op, Proxy } from './keys'
import { buildUrl } from './client'

export type LiveStatus = 'connecting' | 'live' | 'reconnecting' | 'unsupported'

export interface LiveState {
  status: LiveStatus
  nodeId: string | null
  lastEventAt: number | null
  connectedSince: number | null
  reconnects: number
  stale: boolean
}

export const STALE_AFTER_MS = 120_000

const INITIAL: LiveState = {
  status: 'connecting',
  nodeId: null,
  lastEventAt: null,
  connectedSince: null,
  reconnects: 0,
  stale: false,
}

const LiveCtx = createContext<LiveState>({ ...INITIAL, status: 'unsupported' })

type PatchFields = Record<string, unknown>

function mergeProxy(prev: Proxy, fields: PatchFields): Proxy {
  return { ...prev, ...(fields as Partial<Proxy>) }
}

function mergeDongle(prev: Dongle, fields: PatchFields): Dongle {
  return { ...prev, ...(fields as Partial<Dongle>) }
}

function applyProxyPatch(qc: QueryClient, id: string, fields: PatchFields): void {
  qc.setQueriesData<{ items: Proxy[]; total?: number }>({ queryKey: ['proxies'] }, (prev) => {
    if (!prev) return prev
    let touched = false
    const items = prev.items.map((p) => {
      if (p.id !== id) return p
      touched = true
      return mergeProxy(p, fields)
    })
    return touched ? { ...prev, items } : prev
  })
  qc.setQueryData<{ proxy: Proxy }>(qk.proxy(id), (prev) =>
    prev ? { ...prev, proxy: mergeProxy(prev.proxy, fields) } : prev,
  )
}

function applyDonglePatch(qc: QueryClient, id: string, fields: PatchFields): void {
  qc.setQueriesData<{ items: Dongle[] }>({ queryKey: ['dongles'] }, (prev) => {
    if (!prev) return prev
    let touched = false
    const items = prev.items.map((d) => {
      if (d.id !== id) return d
      touched = true
      return mergeDongle(d, fields)
    })
    return touched ? { ...prev, items } : prev
  })
  qc.setQueryData<{ dongle: Dongle }>(qk.dongle(id), (prev) =>
    prev ? { ...prev, dongle: mergeDongle(prev.dongle, fields) } : prev,
  )
}

function applyOperation(qc: QueryClient, op: Op, done: boolean): void {
  qc.setQueryData(qk.operation(op.id), op)
  if (!done) return
  void qc.invalidateQueries({ queryKey: ['operations'] })
  void qc.invalidateQueries({ queryKey: ['rotations'] })
  if (op.subject_type === 'proxy') {
    void qc.invalidateQueries({ queryKey: ['proxies'] })
    void qc.invalidateQueries({ queryKey: qk.proxy(op.subject_id) })
  }
  if (op.subject_type === 'dongle' || op.subject_type === 'slot') {
    void qc.invalidateQueries({ queryKey: ['dongles'] })
    void qc.invalidateQueries({ queryKey: ['slots'] })
  }
}

export interface NoticePayload {
  level: 'info' | 'warn' | 'error'
  title: string
  detail?: string
}

export function dispatchEvent(
  qc: QueryClient,
  ev: AnyEvent,
  onNotice?: (n: NoticePayload) => void,
): void {
  switch (ev.type) {
    case 'hello':
      void qc.invalidateQueries()
      return
    case 'proxy.patch':
      applyProxyPatch(qc, ev.data.id, ev.data.fields as PatchFields)
      return
    case 'dongle.patch':
      applyDonglePatch(qc, ev.data.id, ev.data.fields as PatchFields)
      return
    case 'op.update':
      applyOperation(qc, ev.data, false)
      return
    case 'op.done':
      applyOperation(qc, ev.data, true)
      return
    case 'sms.received':
      void qc.invalidateQueries({ queryKey: ['dongle', ev.data.dongle_id, 'sms'] })
      void qc.invalidateQueries({ queryKey: qk.dongle(ev.data.dongle_id) })
      return
    case 'system.notice':
      onNotice?.({ level: ev.data.level, title: ev.data.title, detail: ev.data.detail })
      return
  }
}

function parseEnvelope(type: string, raw: string): AnyEvent | null {
  if (!isEventType(type)) return null
  try {
    const parsed = JSON.parse(raw) as Partial<EventEnvelope>
    if (parsed && typeof parsed === 'object' && 'data' in parsed) {
      return { ...parsed, type } as AnyEvent
    }
    return null
  } catch {
    return null
  }
}

export interface LiveProviderProps {
  topics: readonly Topic[]
  onNotice?: (n: NoticePayload) => void
  children: ReactNode
}

export function LiveProvider({ topics, onNotice, children }: LiveProviderProps) {
  const qc = useQueryClient()
  const [state, setState] = useState<LiveState>(INITIAL)
  const [tick, setTick] = useState(0)
  const noticeRef = useRef(onNotice)
  noticeRef.current = onNotice
  const topicKey = topics.join(',')

  useEffect(() => {
    const Source = (globalThis as { EventSource?: typeof EventSource }).EventSource
    if (!Source) {
      setState((s) => ({ ...s, status: 'unsupported' }))
      return
    }
    const src = new Source(buildUrl(EVENTS_PATH, { topics: topicKey }), { withCredentials: true })

    const mark = () => setState((s) => ({ ...s, lastEventAt: Date.now(), stale: false }))

    const onMessage = (type: string) => (raw: MessageEvent<string>) => {
      const ev = parseEnvelope(type, raw.data)
      mark()
      if (!ev) return
      if (ev.type === 'hello') {
        setState((s) => ({
          ...s,
          status: 'live',
          nodeId: ev.data.node_id,
          connectedSince: Date.now(),
          reconnects: s.connectedSince == null ? s.reconnects : s.reconnects + 1,
        }))
      }
      dispatchEvent(qc, ev, noticeRef.current)
    }

    const listeners = EVENT_TYPES.map((t) => {
      const fn = onMessage(t) as EventListener
      src.addEventListener(t, fn)
      return [t, fn] as const
    })

    src.onerror = () => {
      setState((s) => ({ ...s, status: 'reconnecting', connectedSince: null }))
    }

    return () => {
      for (const [t, fn] of listeners) src.removeEventListener(t, fn)
      src.onerror = null
      src.close()
    }
  }, [qc, topicKey])

  useEffect(() => {
    const h = setInterval(() => setTick((n) => n + 1), 5000)
    return () => clearInterval(h)
  }, [])

  const value = useMemo(() => {
    void tick
    const age = state.lastEventAt == null ? null : Date.now() - state.lastEventAt
    return { ...state, stale: state.status === 'live' && age != null && age > STALE_AFTER_MS }
  }, [state, tick])

  return createElement(LiveCtx.Provider, { value }, children)
}

export function useLive(): LiveState {
  return useContext(LiveCtx)
}

export function useIsLive(): boolean {
  return useLive().status === 'live'
}
