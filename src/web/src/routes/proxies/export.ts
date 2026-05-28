import type { Proxy } from '../../api/keys'
import { usesUserPass } from './format'

export type Scheme = 'socks5' | 'http'
export type ExportFormat = 'txt' | 'csv'

export interface ExportRow {
  id: string
  host: string
  port: number
  username: string
  password: string
}

export interface SkippedProxy {
  id: string
  reason: string
}

export interface ExportResult {
  text: string
  rows: ExportRow[]
  skipped: SkippedProxy[]
}

export function schemePort(p: Pick<Proxy, 'socks_port' | 'http_port'>, scheme: Scheme): number {
  return scheme === 'socks5' ? p.socks_port : p.http_port
}

export function proxyUri(p: Proxy, scheme: Scheme): string {
  const port = schemePort(p, scheme)
  if (!usesUserPass(p)) return `${scheme}://${p.host}:${port}`
  const user = encodeURIComponent(p.username)
  const pass = encodeURIComponent(p.password ?? '')
  return `${scheme}://${user}:${pass}@${p.host}:${port}`
}

function csvCell(v: string): string {
  return /[",\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v
}

export function buildExport(
  proxies: readonly Proxy[],
  scheme: Scheme,
  format: ExportFormat,
): ExportResult {
  const rows: ExportRow[] = []
  const skipped: SkippedProxy[] = []

  for (const p of proxies) {
    const port = schemePort(p, scheme)
    if (!port) {
      skipped.push({ id: p.id, reason: `no ${scheme} port` })
      continue
    }
    if (!usesUserPass(p)) {
      rows.push({ id: p.id, host: p.host, port, username: '', password: '' })
      continue
    }
    if (!p.password) {
      skipped.push({ id: p.id, reason: 'password not returned by the API' })
      continue
    }
    rows.push({ id: p.id, host: p.host, port, username: p.username, password: p.password })
  }

  const text =
    format === 'csv'
      ? ['host,port,username,password']
          .concat(
            rows.map((r) =>
              [r.host, String(r.port), r.username, r.password].map(csvCell).join(','),
            ),
          )
          .join('\n')
      : rows
          .map((r) => (r.username === '' ? `${r.host}:${r.port}` : `${r.host}:${r.port}:${r.username}:${r.password}`))
          .join('\n')

  return { text, rows, skipped }
}
