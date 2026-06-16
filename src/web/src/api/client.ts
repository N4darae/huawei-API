import { API_BASE } from './keys'
import type { ApiError as ApiErrorBody, OpInProgress } from './keys'

export type HttpMethod = 'GET' | 'POST' | 'PATCH' | 'DELETE'

export type QueryValue = string | number | boolean | undefined | null

export interface RequestInitLite {
  body?: unknown
  query?: Record<string, QueryValue>
  signal?: AbortSignal
  accept?: string
}

export class ApiFailure extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | undefined
  readonly retryAfterS: number | undefined
  readonly operationId: string | undefined

  constructor(init: {
    status: number
    code: string
    message: string
    requestId?: string
    retryAfterS?: number
    operationId?: string
  }) {
    super(init.message)
    this.name = 'ApiFailure'
    this.status = init.status
    this.code = init.code
    this.requestId = init.requestId
    this.retryAfterS = init.retryAfterS
    this.operationId = init.operationId
  }

  get isOpInProgress(): boolean {
    return this.status === 409 && this.operationId != null
  }

  get isRateLimited(): boolean {
    return this.status === 429
  }

  get isUnauthorized(): boolean {
    return this.status === 401
  }

  get isSimPinLocked(): boolean {
    return /sim[_ -]?p(i|u)[nk][_ -]?(required|locked)/i.test(this.code + ' ' + this.message)
  }
}

let csrfToken: string | null = null

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}

export function buildUrl(path: string, query?: Record<string, QueryValue>): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === null || v === '') continue
    params.set(k, String(v))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

async function readError(res: Response): Promise<ApiFailure> {
  let code = `http_${res.status}`
  let message = res.statusText || `request failed with ${res.status}`
  let requestId: string | undefined
  let operationId: string | undefined
  let retryAfterS: number | undefined

  const header = res.headers.get('Retry-After')
  if (header) {
    const n = Number(header)
    if (Number.isFinite(n)) retryAfterS = n
  }

  try {
    const parsed: unknown = await res.json()
    if (isPlainObject(parsed)) {
      const body = parsed as Partial<ApiErrorBody> & Partial<OpInProgress>
      if (typeof body.error === 'string') code = body.error
      if (typeof body.message === 'string' && body.message !== '') message = body.message
      if (typeof body.request_id === 'string') requestId = body.request_id
      if (typeof body.operation_id === 'string') operationId = body.operation_id
      if (typeof body.retry_after === 'number' && retryAfterS === undefined) retryAfterS = body.retry_after
    }
  } catch {
    message = message || 'unreadable error body'
  }

  return new ApiFailure({ status: res.status, code, message, requestId, retryAfterS, operationId })
}

async function send(method: HttpMethod, path: string, init: RequestInitLite = {}): Promise<Response> {
  const headers: Record<string, string> = { Accept: init.accept ?? 'application/json' }
  if (init.body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken

  const res = await fetch(buildUrl(path, init.query), {
    method,
    headers,
    credentials: 'same-origin',
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
    signal: init.signal ?? null,
  })

  if (!res.ok) throw await readError(res)
  return res
}

export async function apiJson<T>(method: HttpMethod, path: string, init?: RequestInitLite): Promise<T> {
  const res = await send(method, path, init)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (text === '') return undefined as T
  return JSON.parse(text) as T
}

export async function apiVoid(method: HttpMethod, path: string, init?: RequestInitLite): Promise<void> {
  await send(method, path, init)
}

export async function apiText(path: string, init?: RequestInitLite): Promise<string> {
  const res = await send('GET', path, { ...init, accept: init?.accept ?? 'text/plain' })
  return res.text()
}

export const url = {
  healthz: () => `${API_BASE}/healthz`,
  login: () => `${API_BASE}/auth/login`,
  logout: () => `${API_BASE}/auth/logout`,
  session: () => `${API_BASE}/auth/session`,

  proxies: () => `${API_BASE}/proxies`,
  proxiesExport: () => `${API_BASE}/proxies/export`,
  proxy: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}`,
  proxyRotate: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/rotate`,
  proxyAuth: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/auth`,
  proxyAuthIPs: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/auth-ips`,
  proxyPorts: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/ports`,
  proxyEnable: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/enable`,
  proxyCustomer: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/customer`,
  proxySelftest: (id: string) => `${API_BASE}/proxies/${encodeURIComponent(id)}/selftest`,

  slots: () => `${API_BASE}/slots`,
  dongles: () => `${API_BASE}/dongles`,
  dongle: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}`,
  dongleReboot: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/reboot`,
  dongleNetMode: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/netmode`,
  dongleLanIP: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/lanip`,
  dongleSms: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/sms`,
  dongleSmsSend: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/sms/send`,
  dongleSmsDelete: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/sms/delete`,
  dongleSmsRead: (id: string) => `${API_BASE}/dongles/${encodeURIComponent(id)}/sms/read`,

  operations: () => `${API_BASE}/operations`,
  operation: (id: string) => `${API_BASE}/operations/${encodeURIComponent(id)}`,
  rotations: () => `${API_BASE}/rotations`,

  customers: () => `${API_BASE}/customers`,

  keys: () => `${API_BASE}/keys`,
  key: (id: string) => `${API_BASE}/keys/${encodeURIComponent(id)}`,
  keyLinkTokens: (id: string) => `${API_BASE}/keys/${encodeURIComponent(id)}/link-tokens`,
  linkToken: (id: string) => `${API_BASE}/link-tokens/${encodeURIComponent(id)}`,
} as const
