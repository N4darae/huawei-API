import type { Tone } from '../../design'
import type { Proxy, ProxyState } from '../../api/keys'

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  let v = n
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  const unit = UNITS[i] ?? 'B'
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${unit}`
}

export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.round(s - m * 60)}s`
}

export function formatSeconds(s: number): string {
  if (s < 60) return `${Math.max(0, Math.ceil(s))}s`
  const m = Math.floor(s / 60)
  return `${m}m ${Math.ceil(s - m * 60)}s`
}

export function formatClock(ms: number | null | undefined): string {
  if (ms == null || ms <= 0) return '—'
  return new Date(ms).toISOString().replace('T', ' ').slice(0, 19) + 'Z'
}

export function formatAgo(ms: number | null | undefined, now: number): string {
  if (ms == null || ms <= 0) return 'never'
  const d = Math.max(0, now - ms)
  if (d < 1000) return 'just now'
  return formatSeconds(d / 1000) + ' ago'
}

export interface Expiry {
  text: string
  tone: Tone
}

export function formatExpiry(expiresAt: number | null | undefined, now: number): Expiry {
  if (expiresAt == null || expiresAt <= 0) return { text: 'no expiry', tone: 'neutral' }
  const days = (expiresAt - now) / 86_400_000
  if (days < 0) return { text: `expired ${Math.max(1, Math.floor(-days))}d ago`, tone: 'danger' }
  if (days < 1) return { text: `in ${Math.max(1, Math.round((expiresAt - now) / 3_600_000))}h`, tone: 'danger' }
  if (days <= 3) return { text: `in ${Math.floor(days)}d`, tone: 'warn' }
  return { text: `in ${Math.floor(days)}d`, tone: 'neutral' }
}

export const PROXY_STATE_TONE: Record<ProxyState, Tone> = {
  active: 'ok',
  suspended: 'warn',
  disabled: 'neutral',
  expired: 'danger',
  degraded: 'warn',
  unknown: 'neutral',
}

export function signalText(bars: number | undefined): string {
  if (bars == null) return 'unknown'
  const b = Math.max(0, Math.min(5, bars))
  return `${'▮'.repeat(b)}${'▯'.repeat(5 - b)} ${b}/5`
}

export function signalTone(bars: number | undefined): Tone {
  if (bars == null) return 'neutral'
  if (bars <= 1) return 'danger'
  if (bars === 2) return 'warn'
  return 'ok'
}

export function usesUserPass(p: Pick<Proxy, 'auth_mode'>): boolean {
  return p.auth_mode === 'userpass' || p.auth_mode === 'both'
}

export function usesIPList(p: Pick<Proxy, 'auth_mode'>): boolean {
  return p.auth_mode === 'iplist' || p.auth_mode === 'both'
}

export const AUTH_MODE_LABEL: Record<Proxy['auth_mode'], string> = {
  userpass: 'user + password',
  iplist: 'IP whitelist',
  both: 'user + password, IP whitelist',
}
