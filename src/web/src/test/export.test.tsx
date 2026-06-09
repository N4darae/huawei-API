import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { buildExport, proxyUri } from '../routes/proxies/export'
import { ExportPanel } from '../routes/proxies/ExportPanel'
import { renderApp } from './render'
import { freshDb } from './state'

const proxies = freshDb().proxies

describe('export format', () => {
  it('emits host:port:user:pass one proxy per line for socks5', () => {
    const { text, rows } = buildExport(proxies, 'socks5', 'txt')
    expect(rows.map((r) => r.port)).toEqual([21001, 21002, 21003])
    expect(text.split('\n')[0]).toBe('203.0.113.10:21001:cust_px01:Kq7mZr2xTn9wLb4V')
  })

  it('switches to the http port without touching the credentials', () => {
    const { text } = buildExport(proxies, 'http', 'txt')
    expect(text.split('\n')[0]).toBe('203.0.113.10:22001:cust_px01:Kq7mZr2xTn9wLb4V')
  })

  it('emits host:port only for an IP-whitelisted proxy that has no password', () => {
    const { text } = buildExport(proxies, 'socks5', 'txt')
    expect(text.split('\n')).toContain('203.0.113.10:21003')
  })

  it('leaves out a userpass proxy whose password the API did not return', () => {
    const { rows, skipped } = buildExport(proxies, 'socks5', 'txt')
    expect(rows.map((r) => r.id)).not.toContain('px04')
    expect(skipped).toEqual([{ id: 'px04', reason: 'password not returned by the API' }])
  })

  it('emits a CSV with a header row and the same four fields', () => {
    const { text } = buildExport(proxies, 'socks5', 'csv')
    const lines = text.split('\n')
    expect(lines[0]).toBe('host,port,username,password')
    expect(lines[1]).toBe('203.0.113.10,21001,cust_px01,Kq7mZr2xTn9wLb4V')
  })

  it('builds copyable proxy URIs for both schemes', () => {
    const p = proxies[0]
    if (!p) throw new Error('fixture missing')
    expect(proxyUri(p, 'socks5')).toBe('socks5://cust_px01:Kq7mZr2xTn9wLb4V@203.0.113.10:21001')
    expect(proxyUri(p, 'http')).toBe('http://cust_px01:Kq7mZr2xTn9wLb4V@203.0.113.10:22001')
  })
})

describe('export panel', () => {
  it('previews the list and links to the server export with the same parameters', async () => {
    const user = userEvent.setup()
    renderApp(<ExportPanel open proxies={proxies} onClose={() => {}} />)

    const area = screen.getByLabelText(/of 4 proxies/) as HTMLTextAreaElement
    expect(area.value.startsWith('203.0.113.10:21001:cust_px01:Kq7mZr2xTn9wLb4V')).toBe(true)

    await user.click(screen.getByLabelText('http'))
    expect((screen.getByLabelText(/of 4 proxies/) as HTMLTextAreaElement).value).toContain(':22001:')

    const link = screen.getByRole('link', { name: 'Download from server' }) as HTMLAnchorElement
    expect(link.getAttribute('href')).toContain('/api/v1/proxies/export?format=txt&scheme=http&ids=')
  })

  it('warns about proxies it had to leave out', async () => {
    renderApp(<ExportPanel open proxies={proxies} onClose={() => {}} />)
    expect(await screen.findByText('1 proxies left out')).toBeTruthy()
    expect(screen.getByText(/px04: password not returned by the API/)).toBeTruthy()
  })
})
