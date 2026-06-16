import { useMemo, useState } from 'react'
import { Button, Drawer, Notice, useToast, writeClipboard } from '../../design'
import { buildUrl, url } from '../../api/client'
import type { Proxy } from '../../api/keys'
import { buildExport } from './export'
import type { ExportFormat, Scheme } from './export'

export interface ExportPanelProps {
  open: boolean
  proxies: readonly Proxy[]
  onClose: () => void
}

export function ExportPanel({ open, proxies, onClose }: ExportPanelProps) {
  const [scheme, setScheme] = useState<Scheme>('socks5')
  const [format, setFormat] = useState<ExportFormat>('txt')
  const toast = useToast()

  const result = useMemo(() => buildExport(proxies, scheme, format), [proxies, scheme, format])
  const ids = useMemo(() => proxies.map((p) => p.id).join(','), [proxies])
  const href = buildUrl(url.proxiesExport(), { format, scheme, ids })

  return (
    <Drawer open={open} title="Export proxy list" onClose={onClose}>
      <div className="row">
        <fieldset className="field" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="field-label">Scheme</legend>
          <div className="row">
            {(['socks5', 'http'] as const).map((s) => (
              <label key={s} className="row">
                <input
                  type="radio"
                  name="export-scheme"
                  checked={scheme === s}
                  onChange={() => setScheme(s)}
                />
                <span>{s}</span>
              </label>
            ))}
          </div>
        </fieldset>
        <fieldset className="field" style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="field-label">Format</legend>
          <div className="row">
            {(['txt', 'csv'] as const).map((f) => (
              <label key={f} className="row">
                <input
                  type="radio"
                  name="export-format"
                  checked={format === f}
                  onChange={() => setFormat(f)}
                />
                <span>{f === 'txt' ? 'host:port:user:pass' : 'CSV'}</span>
              </label>
            ))}
          </div>
        </fieldset>
      </div>

      <div className="col">
        <label className="field-label" htmlFor="export-text">
          {result.rows.length} of {proxies.length} proxies
        </label>
        <textarea
          id="export-text"
          className="field-input"
          readOnly
          rows={14}
          value={result.text}
          spellCheck={false}
        />
      </div>

      {result.skipped.length > 0 ? (
        <Notice tone="warn" title={`${result.skipped.length} proxies left out`}>
          {result.skipped.map((s) => `${s.id}: ${s.reason}`).join(' · ')}
        </Notice>
      ) : null}

      <div className="row">
        <Button
          variant="primary"
          onClick={() => {
            void writeClipboard(result.text).then((ok) =>
              toast.push({
                tone: ok ? 'ok' : 'danger',
                title: ok ? `Copied ${result.rows.length} lines` : 'Clipboard unavailable',
                detail: ok ? undefined : 'Select the text and copy it manually.',
              }),
            )
          }}
        >
          Copy list
        </Button>
        <a className="btn" href={href} download={`proxies.${format}`}>
          Download from server
        </a>
      </div>
    </Drawer>
  )
}
