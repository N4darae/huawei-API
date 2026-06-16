import { HttpResponse, http } from 'msw'
import type { HttpHandler } from 'msw'
import { readSpecRoutes, routeKey, toMswPath } from './spec'
import type { SpecRoute } from './spec'
import { db, installRotateTape, readTape } from './state'
import { buildExport } from '../routes/proxies/export'
import type { Scheme } from '../routes/proxies/export'

type Resolver = Parameters<typeof http.get>[1]

function findProxy(id: string | readonly string[] | undefined) {
  return db.proxies.find((p) => p.id === id)
}

function findDongle(id: string | readonly string[] | undefined) {
  return db.dongles.find((d) => d.id === id)
}

function param(v: string | readonly string[] | undefined): string {
  return Array.isArray(v) ? ((v[0] as string) ?? '') : ((v as string) ?? '')
}

async function jsonBody(request: Request): Promise<Record<string, unknown>> {
  try {
    return (await request.json()) as Record<string, unknown>
  } catch {
    return {}
  }
}

export const RESOLVERS: Record<string, Resolver> = {
  'GET /api/v1/healthz': () => HttpResponse.json(db.health),

  'POST /api/v1/auth/login': async ({ request }) => {
    const body = await jsonBody(request)
    if (body['password'] !== 'correct-horse') {
      return HttpResponse.json({ error: 'invalid_credentials', message: 'bad username or password' }, { status: 401 })
    }
    db.session = { username: String(body['username']), expires_at: db.now + 3_600_000, csrf_token: 'csrf-token-1' }
    return new HttpResponse(null, { status: 204 })
  },

  'POST /api/v1/auth/logout': () => {
    db.session = null
    return new HttpResponse(null, { status: 204 })
  },

  'GET /api/v1/auth/session': () =>
    db.session
      ? HttpResponse.json(db.session)
      : HttpResponse.json({ error: 'unauthorized', message: 'no session' }, { status: 401 }),

  'GET /api/v1/proxies': ({ request }) => {
    const q = new URL(request.url).searchParams
    let items = db.proxies
    const state = q.get('state')
    if (state) items = items.filter((p) => p.state === state)
    const within = q.get('expiring_within_days')
    if (within) {
      const limit = db.now + Number(within) * 86_400_000
      items = items.filter((p) => p.expires_at != null && p.expires_at <= limit)
    }
    return HttpResponse.json({ items, total: items.length })
  },

  'GET /api/v1/proxies/export': ({ request }) => {
    const q = new URL(request.url).searchParams
    const format = q.get('format') === 'csv' ? 'csv' : 'txt'
    const scheme: Scheme = q.get('scheme') === 'http' ? 'http' : 'socks5'
    const ids = q.get('ids')
    const wanted = ids ? new Set(ids.split(',')) : null
    const list = wanted ? db.proxies.filter((p) => wanted.has(p.id)) : db.proxies
    const { text } = buildExport(list, scheme, format)
    return new HttpResponse(text, {
      headers: { 'Content-Type': format === 'csv' ? 'text/csv' : 'text/plain' },
    })
  },

  'GET /api/v1/proxies/{proxy_id}': ({ params }) => {
    const p = findProxy(params['proxy_id'])
    if (!p) return HttpResponse.json({ error: 'not_found', message: 'no such proxy' }, { status: 404 })
    return HttpResponse.json({
      proxy: p,
      auth_ips: db.authIps[p.id] ?? [],
      slot: db.slots.find((s) => s.slot === p.slot),
      last_rotation: db.rotations[0],
    })
  },

  'POST /api/v1/proxies/{proxy_id}/rotate': ({ params }) => {
    const id = param(params['proxy_id'])
    const plan = db.rotateResponse
    if (plan.kind === 'conflict') {
      return HttpResponse.json(
        { error: 'op_in_progress', operation_id: plan.opId, poll_url: `/api/v1/operations/${plan.opId}` },
        { status: 409 },
      )
    }
    if (plan.kind === 'rate_limited') {
      return HttpResponse.json(
        { error: 'rate_limited', message: 'per-proxy minimum interval not met', retry_after: plan.retryAfter ?? 60 },
        { status: 429, headers: { 'Retry-After': String(plan.retryAfter ?? 60) } },
      )
    }
    const opId = plan.opId ?? (db.ops[`op-rotate-${id}`] ? `op-rotate-${id}` : installRotateTape({ proxyId: id }))
    const p = findProxy(id)
    if (p) p.active_operation_id = opId
    return HttpResponse.json(
      { operation_id: opId, poll_url: `/api/v1/operations/${opId}`, state: 'running', deadline_at: db.now + 90_000 },
      { status: 202 },
    )
  },

  'POST /api/v1/proxies/{proxy_id}/auth': async ({ params, request }) => {
    const p = findProxy(params['proxy_id'])
    const body = await jsonBody(request)
    if (!p) return HttpResponse.json({ error: 'not_found', message: 'no such proxy' }, { status: 404 })
    p.auth_mode = body['auth_mode'] as typeof p.auth_mode
    if (typeof body['username'] === 'string') p.username = body['username']
    db.requests.push(`setAuth ${p.id} ${JSON.stringify(body)}`)
    return HttpResponse.json({ operation_id: 'op-auth-1', poll_url: '/api/v1/operations/op-auth-1' }, { status: 202 })
  },

  'GET /api/v1/proxies/{proxy_id}/auth-ips': ({ params }) =>
    HttpResponse.json({ items: db.authIps[param(params['proxy_id'])] ?? [] }),

  'POST /api/v1/proxies/{proxy_id}/auth-ips': async ({ params, request }) => {
    const id = param(params['proxy_id'])
    const body = await jsonBody(request)
    const cidr = String(body['cidr'] ?? '')
    const list = db.authIps[id] ?? []
    list.push({ id: `aip-${list.length + 10}`, cidr, note: String(body['note'] ?? ''), created_at: db.now })
    db.authIps[id] = list
    return HttpResponse.json({ items: list })
  },

  'DELETE /api/v1/proxies/{proxy_id}/auth-ips': async ({ params, request }) => {
    const id = param(params['proxy_id'])
    const body = await jsonBody(request)
    const list = (db.authIps[id] ?? []).filter((x) => x.cidr !== body['cidr'])
    db.authIps[id] = list
    return HttpResponse.json({ items: list })
  },

  'POST /api/v1/proxies/{proxy_id}/ports': async ({ params, request }) => {
    const p = findProxy(params['proxy_id'])
    const body = await jsonBody(request)
    if (p) p.policy = body as typeof p.policy
    return HttpResponse.json({ operation_id: 'op-ports-1', poll_url: '/api/v1/operations/op-ports-1' }, { status: 202 })
  },

  'POST /api/v1/proxies/{proxy_id}/enable': async ({ params, request }) => {
    const p = findProxy(params['proxy_id'])
    const body = await jsonBody(request)
    if (!p) return HttpResponse.json({ error: 'not_found', message: 'no such proxy' }, { status: 404 })
    p.enabled = Boolean(body['enabled'])
    return HttpResponse.json(p)
  },

  'POST /api/v1/proxies/{proxy_id}/customer': async ({ params, request }) => {
    const p = findProxy(params['proxy_id'])
    const body = await jsonBody(request)
    if (!p) return HttpResponse.json({ error: 'not_found', message: 'no such proxy' }, { status: 404 })
    p.customer_id = (body['customer_id'] as string | null) ?? null
    return HttpResponse.json(p)
  },

  'POST /api/v1/proxies/{proxy_id}/selftest': ({ params }) => {
    const p = findProxy(params['proxy_id'])
    return HttpResponse.json({
      socks_ok: true,
      http_ok: true,
      egress_ip: p?.wan_ip ?? '100.71.4.5',
      latency_ms: 180,
    })
  },

  'GET /api/v1/slots': () => HttpResponse.json({ items: db.slots }),

  'GET /api/v1/dongles': () => HttpResponse.json({ items: db.dongles }),

  'GET /api/v1/dongles/{dongle_id}': ({ params }) => {
    const id = param(params['dongle_id'])
    const forced = db.dongleGetStatus[id]
    if (forced) {
      return HttpResponse.json({ error: 'device_unreachable', message: 'no answer from the dongle' }, { status: forced })
    }
    const d = findDongle(id)
    if (!d) return HttpResponse.json({ error: 'not_found', message: 'no such dongle' }, { status: 404 })
    return HttpResponse.json({
      dongle: d,
      signal: db.signal[id],
      traffic: db.traffic[id],
      slot: db.slots.find((s) => s.dongle_id === id),
      unread_sms: (db.sms[id] ?? []).filter((m) => !m.read).length,
    })
  },

  'PATCH /api/v1/dongles/{dongle_id}': async ({ params, request }) => {
    const d = findDongle(params['dongle_id'])
    const body = await jsonBody(request)
    if (!d) return HttpResponse.json({ error: 'not_found', message: 'no such dongle' }, { status: 404 })
    Object.assign(d, body)
    return HttpResponse.json(d)
  },

  'POST /api/v1/dongles/{dongle_id}/reboot': ({ params }) =>
    HttpResponse.json(
      { operation_id: `op-reboot-${param(params['dongle_id'])}`, poll_url: '/api/v1/operations/x' },
      { status: 202 },
    ),

  'POST /api/v1/dongles/{dongle_id}/netmode': async ({ params, request }) => {
    const d = findDongle(params['dongle_id'])
    const body = await jsonBody(request)
    if (d) d.net_mode = body['net_mode'] as typeof d.net_mode
    return HttpResponse.json(
      { operation_id: `op-netmode-${param(params['dongle_id'])}`, poll_url: '/api/v1/operations/x' },
      { status: 202 },
    )
  },

  'POST /api/v1/dongles/{dongle_id}/lanip': ({ params }) =>
    HttpResponse.json(
      { operation_id: `op-lanip-${param(params['dongle_id'])}`, poll_url: '/api/v1/operations/x' },
      { status: 202 },
    ),

  'GET /api/v1/dongles/{dongle_id}/sms': ({ params, request }) => {
    const id = param(params['dongle_id'])
    const box = Number(new URL(request.url).searchParams.get('box') ?? '1')
    const items = (db.sms[id] ?? []).filter((m) => m.box === box)
    return HttpResponse.json({ items, total: items.length })
  },

  'POST /api/v1/dongles/{dongle_id}/sms/send': async ({ params, request }) => {
    const id = param(params['dongle_id'])
    const body = await jsonBody(request)
    db.requests.push(`sendSms ${id} ${JSON.stringify(body)}`)
    return new HttpResponse(null, { status: 204 })
  },

  'POST /api/v1/dongles/{dongle_id}/sms/delete': async ({ params, request }) => {
    const id = param(params['dongle_id'])
    const body = await jsonBody(request)
    db.sms[id] = (db.sms[id] ?? []).filter((m) => m.index !== body['index'])
    return new HttpResponse(null, { status: 204 })
  },

  'POST /api/v1/dongles/{dongle_id}/sms/read': async ({ params, request }) => {
    const id = param(params['dongle_id'])
    const body = await jsonBody(request)
    for (const m of db.sms[id] ?? []) if (m.index === body['index']) m.read = true
    return new HttpResponse(null, { status: 204 })
  },

  'GET /api/v1/operations': () =>
    HttpResponse.json({ items: Object.values(db.ops).map((t) => t.frames[t.cursor] ?? t.frames[0]) }),

  'GET /api/v1/operations/{op_id}': ({ params }) => {
    const op = readTape(param(params['op_id']))
    if (!op) return HttpResponse.json({ error: 'not_found', message: 'no such operation' }, { status: 404 })
    return HttpResponse.json(op)
  },

  'GET /api/v1/rotations': () => HttpResponse.json({ items: db.rotations }),

  'GET /api/v1/keys': () => HttpResponse.json({ items: db.keys }),

  'POST /api/v1/keys': async ({ request }) => {
    const body = await jsonBody(request)
    const key = {
      id: `key-${db.keys.length + 1}`,
      name: String(body['name']),
      prefix: 'dgl_zz99',
      customer_id: (body['customer_id'] as string | undefined) ?? null,
      scopes: (body['scopes'] as string[]) ?? [],
      proxy_ids: (body['proxy_ids'] as string[]) ?? [],
      last_used_at: null,
      revoked_at: null,
      created_at: db.now,
      link_tokens: [],
    }
    db.keys.push(key)
    return HttpResponse.json({ key, secret: 'dgl_zz99_THISISTHEONLYTIMEYOUSEEIT' }, { status: 201 })
  },

  'DELETE /api/v1/keys/{key_id}': ({ params }) => {
    const k = db.keys.find((x) => x.id === param(params['key_id']))
    if (k) k.revoked_at = db.now
    return new HttpResponse(null, { status: 204 })
  },

  'POST /api/v1/keys/{key_id}/link-tokens': ({ params }) => {
    const id = param(params['key_id'])
    const k = db.keys.find((x) => x.id === id)
    const token = { id: `lt-${(k?.link_tokens?.length ?? 0) + 2}`, api_key_id: id, revoked_at: null, created_at: db.now }
    if (k) k.link_tokens = [...(k.link_tokens ?? []), token]
    return HttpResponse.json({ token, url: '/r/9f3c1d2e8a7b4c5d6e0f' }, { status: 201 })
  },

  'DELETE /api/v1/link-tokens/{token_id}': ({ params }) => {
    const id = param(params['token_id'])
    for (const k of db.keys) {
      for (const t of k.link_tokens ?? []) if (t.id === id) t.revoked_at = db.now
    }
    return new HttpResponse(null, { status: 204 })
  },
}

export const NOT_CALLED_BY_SPA = new Set<string>([
  'GET /api/v1/customers',
  'POST /api/v1/customers',
  'PATCH /api/v1/customers/{customer_id}',
  'GET /api/v1/events',
  'POST /api/v1/rotate/{proxy_id}',
  'GET /api/v1/status/{proxy_id}',
  'GET /r/{link_token}',
  'POST /r/{link_token}',
])

function handlerFor(route: SpecRoute, resolver: Resolver): HttpHandler {
  const path = toMswPath(route.path)
  switch (route.method) {
    case 'post':
      return http.post(path, resolver)
    case 'patch':
      return http.patch(path, resolver)
    case 'delete':
      return http.delete(path, resolver)
    case 'put':
      return http.put(path, resolver)
    default:
      return http.get(path, resolver)
  }
}

export function buildHandlers(): HttpHandler[] {
  return readSpecRoutes().map((route) => {
    const key = routeKey(route)
    const resolver =
      RESOLVERS[key] ??
      (() =>
        HttpResponse.json(
          { error: 'not_implemented_in_mock', message: `no mock resolver for ${key}` },
          { status: 501 },
        ))
    return handlerFor(route, resolver)
  })
}

export const handlers = buildHandlers()
