import type { Tone } from '../../design'

export const SIM_STATE_LABEL: Record<number, string> = {
  255: 'no SIM',
  256: 'CPIN error',
  257: 'ready',
  258: 'PIN disabled',
  259: 'PIN checking',
  260: 'PIN required',
  261: 'PUK required',
}

export const SIM_PIN_REQUIRED = 260
export const SIM_PUK_REQUIRED = 261
export const SIM_PIN_CHECKING = 259

export function simStateLabel(s: number | undefined): string {
  if (s == null) return 'unknown'
  return SIM_STATE_LABEL[s] ?? `code ${s}`
}

export function simLocked(s: number | undefined): boolean {
  return s === SIM_PIN_CHECKING || s === SIM_PIN_REQUIRED || s === SIM_PUK_REQUIRED
}

export function simUsable(s: number | undefined): boolean {
  return s === 257 || s === 258
}

export function simTone(s: number | undefined): Tone {
  if (s == null) return 'neutral'
  if (simUsable(s)) return 'ok'
  if (simLocked(s)) return 'danger'
  return 'warn'
}

export const CONN_STATUS_LABEL: Record<number, string> = {
  900: 'connecting',
  901: 'connected',
  902: 'disconnecting',
  903: 'disconnected',
}

export function connStatusLabel(c: number | undefined): string {
  if (c == null) return 'unknown'
  return CONN_STATUS_LABEL[c] ?? `code ${c}`
}

export function connTone(c: number | undefined): Tone {
  if (c === 901) return 'ok'
  if (c === 900 || c === 902) return 'warn'
  if (c === 903) return 'danger'
  return 'neutral'
}
