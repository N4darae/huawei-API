import type { components } from './schema'
import type { Topic } from './events'

export const API_BASE = '/api/v1'
export const LINK_BASE = '/r'
export const EVENTS_PATH = `${API_BASE}/events`

export type Proxy = components['schemas']['Proxy']
export type ProxyDetail = components['schemas']['ProxyDetail']
export type ProxyState = components['schemas']['ProxyState']
export type ProxyPolicy = components['schemas']['ProxyPolicy']
export type AuthIP = components['schemas']['AuthIP']
export type AuthMode = components['schemas']['AuthMode']
export type Slot = components['schemas']['Slot']
export type Dongle = components['schemas']['Dongle']
export type DongleDetail = components['schemas']['DongleDetail']
export type Sms = components['schemas']['Sms']
export type Op = components['schemas']['Operation']
export type OpKind = components['schemas']['OpKind']
export type OpState = components['schemas']['OpState']
export type Trigger = components['schemas']['Trigger']
export type Rotation = components['schemas']['Rotation']
export type RotationResult = components['schemas']['RotationResult']
export type Customer = components['schemas']['Customer']
export type ApiKey = components['schemas']['ApiKey']
export type LinkToken = components['schemas']['LinkToken']
export type SelftestResult = components['schemas']['SelftestResult']
export type EnrollSession = components['schemas']['EnrollSession']
export type Health = components['schemas']['Health']
export type ApiError = components['schemas']['Error']
export type OpInProgress = components['schemas']['OpInProgress']

export interface ProxyQuery {
  customer_id?: string
  state?: ProxyState
  expiring_within_days?: number
}

export interface OperationQuery {
  kind?: OpKind
  trigger?: Trigger
  state?: OpState
  subject_id?: string
  limit?: number
}

export interface SmsQuery {
  box?: 1 | 2 | 3
  page?: number
  size?: number
}

export const qk = {
  health: () => ['health'] as const,
  session: () => ['session'] as const,

  proxies: (q?: ProxyQuery) => ['proxies', q ?? {}] as const,
  proxy: (id: string) => ['proxy', id] as const,
  proxyAuthIPs: (id: string) => ['proxy', id, 'auth-ips'] as const,

  slots: () => ['slots'] as const,
  dongles: () => ['dongles'] as const,
  dongle: (id: string) => ['dongle', id] as const,
  sms: (dongleId: string, q?: SmsQuery) => ['dongle', dongleId, 'sms', q ?? {}] as const,

  operations: (q?: OperationQuery) => ['operations', q ?? {}] as const,
  operation: (id: string) => ['operation', id] as const,
  rotations: (proxyId?: string) => ['rotations', proxyId ?? 'all'] as const,

  customers: () => ['customers'] as const,
  keys: () => ['keys'] as const,
  enroll: (sessionId: string) => ['enroll', sessionId] as const,
} as const

export const TOPIC_QUERY_KEYS: Record<Topic, readonly unknown[][]> = {
  proxies: [[...qk.proxies()], [...qk.slots()]],
  dongles: [[...qk.dongles()], [...qk.slots()]],
  operations: [[...qk.operations()], [...qk.rotations()]],
  sms: [[...qk.dongles()]],
  system: [[...qk.health()]],
}

export const ROTATE_STEPS = [
  'precheck',
  'fence',
  'data_off',
  'hold',
  'data_on',
  'wait_connect',
  'unfence',
  'verify',
  'done',
] as const

export type RotateStep = (typeof ROTATE_STEPS)[number]

export function isStalled(op: Op, now: number): boolean {
  return op.finished_at == null && op.deadline_at > 0 && now > op.deadline_at
}
