import type { components } from '../api/schema'

type ApiKey = components['schemas']['ApiKey']
type AuthIP = components['schemas']['AuthIP']
type Dongle = components['schemas']['Dongle']
type Health = components['schemas']['Health']
type Op = components['schemas']['Operation']
type Proxy = components['schemas']['Proxy']
type Rotation = components['schemas']['Rotation']
type Session = components['schemas']['Session']
type Signal = components['schemas']['Signal']
type Slot = components['schemas']['Slot']
type Sms = components['schemas']['Sms']
type Traffic = components['schemas']['Traffic']

export interface OpTape {
  frames: Op[]
  cursor: number
}

export interface MockDb {
  now: number
  session: Session | null
  proxies: Proxy[]
  authIps: Record<string, AuthIP[]>
  slots: Slot[]
  dongles: Dongle[]
  signal: Record<string, Signal>
  traffic: Record<string, Traffic>
  sms: Record<string, Sms[]>
  ops: Record<string, OpTape>
  rotations: Rotation[]
  keys: ApiKey[]
  health: Health
  rotateResponse: { kind: 'accept' | 'conflict' | 'rate_limited'; opId?: string; retryAfter?: number }
  dongleGetStatus: Record<string, number>
  requests: string[]
}

let T0 = Date.now()

function proxy(over: Partial<Proxy> & Pick<Proxy, 'id' | 'slot'>): Proxy {
  return {
    state: 'active',
    host: '203.0.113.10',
    socks_port: 21000 + over.slot,
    http_port: 22000 + over.slot,
    username: `cust_${over.id}`,
    password: 'Kq7mZr2xTn9wLb4V',
    auth_mode: 'userpass',
    auth_ip_count: 0,
    enabled: true,
    suspended: false,
    customer_id: null,
    wan_ip: '100.71.4.5',
    signal_bars: 4,
    data_used_bytes: 2 * 1024 * 1024 * 1024,
    data_cap_bytes: 30 * 1024 * 1024 * 1024,
    ports_bound: { socks: true, http: true, probe_ok: true },
    policy: { allow_all_ports: true, allowed_ports: [], max_conn: 200, conn_limit: 64 },
    active_operation_id: null,
    updated_at: T0,
    ...over,
  }
}

function dongle(over: Partial<Dongle> & Pick<Dongle, 'id' | 'slot'>): Dongle {
  return {
    imei: '861234567890123',
    iccid: '8984000000000000000',
    imsi: '452010000000000',
    firmware_ver: '22.333.01.00.00',
    hw_ver: 'CL2E3372HM',
    carrier: 'Viettel',
    conn_status: 901,
    sim_state: 257,
    net_mode: 'auto',
    wan_ip: '100.71.4.5',
    lan_ip_change_supported: true,
    hilink_login_required: false,
    auto_recover_enabled: true,
    data_cap_bytes: 30 * 1024 * 1024 * 1024,
    cap_reset_day: 1,
    reachable: true,
    observed_at: T0,
    ...over,
  }
}

function slot(n: number, dongleId: string | null): Slot {
  return {
    id: `slot-${n}`,
    slot: n,
    if_name: `dg0${n}`,
    usb_path: `1-${n}`,
    id_path: `platform-xhci-usb-0:${n}:1.0`,
    occupied: dongleId != null,
    dongle_id: dongleId,
    host_ip: `192.168.${100 + n}.100`,
    gateway_ip: `192.168.${100 + n}.1`,
    route_table: 1000 + n,
  }
}

export function freshDb(): MockDb {
  T0 = Date.now()
  return {
    now: T0,
    session: { username: 'operator', expires_at: T0 + 3_600_000, csrf_token: 'csrf-token-1' },
    proxies: [
      proxy({ id: 'px01', slot: 1 }),
      proxy({
        id: 'px02',
        slot: 2,
        state: 'degraded',
        ports_bound: { socks: true, http: false, probe_ok: false },
        signal_bars: 1,
        wan_ip: '100.71.9.9',
        data_used_bytes: 29 * 1024 * 1024 * 1024,
        customer_id: 'cus-1',
        customer_name: 'Acme',
        expires_at: T0 + 2 * 86_400_000,
      }),
      proxy({
        id: 'px03',
        slot: 3,
        state: 'expired',
        auth_mode: 'iplist',
        password: undefined,
        auth_ip_count: 2,
        expires_at: T0 - 86_400_000,
        wan_ip: '',
        ports_bound: { socks: false, http: false, probe_ok: false },
      }),
      proxy({ id: 'px04', slot: 4, auth_mode: 'both', password: undefined, auth_ip_count: 1 }),
    ],
    authIps: {
      px03: [
        { id: 'aip1', cidr: '203.0.113.5/32', note: 'customer office', created_at: T0 },
        { id: 'aip2', cidr: '198.51.100.0/24', created_at: T0 },
      ],
      px04: [{ id: 'aip3', cidr: '192.0.2.7/32', created_at: T0 }],
    },
    slots: [slot(1, 'dg-1'), slot(2, 'dg-2'), slot(3, null), slot(4, null)],
    dongles: [dongle({ id: 'dg-1', slot: 1 }), dongle({ id: 'dg-2', slot: 2, sim_state: 260, conn_status: 903 })],
    signal: {
      'dg-1': { rssi: -61, rsrp: -95, rsrq: -10, sinr: 12, bars: 4, band: 'B3', mode: 'LTE' },
      'dg-2': { rssi: -101, rsrp: -119, rsrq: -17, sinr: 1, bars: 1, band: 'B1', mode: 'LTE' },
    },
    traffic: {
      'dg-1': { month_download: 8 * 1024 * 1024 * 1024, month_upload: 512 * 1024 * 1024 },
      'dg-2': { month_download: 1024 * 1024 * 1024, month_upload: 64 * 1024 * 1024 },
    },
    sms: {
      'dg-1': [
        {
          index: 1,
          phone: '+84900000001',
          content: 'Your balance is 120000 VND',
          sent_at: T0 - 3600_000,
          box: 1,
          read: false,
          is_fragment: false,
        },
        {
          index: 2,
          phone: '+84900000002',
          content: 'part one of a very long carrier notice that the modem split',
          sent_at: T0 - 7200_000,
          box: 1,
          read: true,
          is_fragment: true,
        },
      ],
      'dg-2': [],
    },
    ops: {},
    rotations: [
      {
        id: 'rot-1',
        requested_at: T0 - 600_000,
        duration_ms: 41_000,
        old_public_ip: '100.71.4.1',
        new_public_ip: '100.71.4.5',
        ip_changed: true,
        result: 'changed',
      },
    ],
    keys: [
      {
        id: 'key-1',
        name: 'Acme production',
        prefix: 'dgl_ab12',
        customer_id: 'cus-1',
        scopes: ['rotate', 'status'],
        proxy_ids: ['px02'],
        last_used_at: T0 - 120_000,
        revoked_at: null,
        created_at: T0 - 86_400_000,
        link_tokens: [{ id: 'lt-existing-1', api_key_id: 'key-1', revoked_at: null, created_at: T0 - 3600_000 }],
      },
    ],
    health: {
      status: 'ok',
      product: 'dongled',
      node_id: 'node-a',
      version: '1.0.0',
      invariants: [
        { name: 'public_src_rule_present', ok: true },
        { name: 'egress_fenced', ok: true },
      ],
    },
    rotateResponse: { kind: 'accept' },
    dongleGetStatus: {},
    requests: [],
  }
}

export let db: MockDb = freshDb()

export function resetDb(): void {
  db = freshDb()
}

export function baseOp(over: Partial<Op> & Pick<Op, 'id' | 'kind' | 'subject_id'>): Op {
  return {
    subject_type: over.kind === 'rotate' ? 'proxy' : 'dongle',
    state: 'running',
    step: 'precheck',
    pct: 0,
    started_at: db.now,
    deadline_at: db.now + 90_000,
    finished_at: null,
    trigger: 'admin_ui',
    ...over,
  }
}

export interface RotateTapeOptions {
  proxyId: string
  opId?: string
  outcome?: 'changed' | 'unchanged' | 'failed'
  stallAtStep?: string
  oldIp?: string
  newIp?: string
  error?: string
}

const ROTATE_SEQUENCE = [
  'precheck',
  'fence',
  'data_off',
  'hold',
  'data_on',
  'wait_connect',
  'unfence',
  'verify',
  'done',
]

export function installRotateTape(opts: RotateTapeOptions): string {
  const id = opts.opId ?? `op-rotate-${opts.proxyId}`
  const frames: Op[] = []
  const stopAt = opts.stallAtStep ? ROTATE_SEQUENCE.indexOf(opts.stallAtStep) : ROTATE_SEQUENCE.length - 1

  for (let i = 0; i <= stopAt; i++) {
    const step = ROTATE_SEQUENCE[i] as string
    frames.push(
      baseOp({
        id,
        kind: 'rotate',
        subject_id: opts.proxyId,
        step,
        pct: Math.round((i / (ROTATE_SEQUENCE.length - 1)) * 100),
      }),
    )
  }

  if (opts.stallAtStep) {
    const last = frames[frames.length - 1] as Op
    frames.push({ ...last, deadline_at: db.now - 1000 })
  } else {
    const outcome = opts.outcome ?? 'changed'
    frames.push(
      baseOp({
        id,
        kind: 'rotate',
        subject_id: opts.proxyId,
        step: 'done',
        pct: 100,
        state: outcome === 'failed' ? 'failed' : 'succeeded',
        finished_at: db.now + 45_000,
        error: opts.error,
        result: {
          result: outcome,
          ip_changed: outcome === 'changed',
          old_ip: opts.oldIp ?? '100.71.4.5',
          new_ip: outcome === 'changed' ? (opts.newIp ?? '100.71.8.8') : (opts.oldIp ?? '100.71.4.5'),
          duration_ms: 45_000,
        },
      }),
    )
  }

  db.ops[id] = { frames, cursor: 0 }
  return id
}

export function installTape(id: string, frames: Op[]): string {
  db.ops[id] = { frames, cursor: 0 }
  return id
}

export function readTape(id: string): Op | null {
  const tape = db.ops[id]
  if (!tape) return null
  const frame = tape.frames[Math.min(tape.cursor, tape.frames.length - 1)]
  if (tape.cursor < tape.frames.length - 1) tape.cursor++
  return frame ?? null
}
