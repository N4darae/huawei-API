import { useState } from 'react'
import { Button, Input, Notice } from '../../design'
import type { Proxy, ProxyPolicy } from '../../api/keys'
import { useSetProxyPorts } from '../../api/query'

interface Range {
  lo: number
  hi: number
}

export const FIREWALL_BLOCKED_PORTS = [25, 465, 587]

export function PortEditor({ proxy }: { proxy: Proxy }) {
  const save = useSetProxyPorts()
  const [allowAll, setAllowAll] = useState(proxy.policy.allow_all_ports)
  const [ranges, setRanges] = useState<Range[]>(proxy.policy.allowed_ports ?? [])
  const [maxConn, setMaxConn] = useState(String(proxy.policy.max_conn))
  const [connLimit, setConnLimit] = useState(String(proxy.policy.conn_limit))
  const [lo, setLo] = useState('')
  const [hi, setHi] = useState('')

  const parsedLo = Number(lo)
  const parsedHi = Number(hi === '' ? lo : hi)
  const addable =
    lo !== '' &&
    Number.isInteger(parsedLo) &&
    Number.isInteger(parsedHi) &&
    parsedLo >= 1 &&
    parsedHi <= 65535 &&
    parsedLo <= parsedHi

  const invalid = !allowAll && ranges.length === 0

  const policy: ProxyPolicy = {
    allow_all_ports: allowAll,
    allowed_ports: allowAll ? [] : ranges,
    max_conn: Number(maxConn) || 0,
    conn_limit: Number(connLimit) || 0,
  }

  return (
    <section className="card">
      <h3 className="card-title">Destination ports</h3>

      <label className="row">
        <input type="checkbox" checked={allowAll} onChange={(e) => setAllowAll(e.target.checked)} />
        <span>Allow all destination ports (default)</span>
      </label>
      <span className="faint">
        Ports {FIREWALL_BLOCKED_PORTS.join(', ')} are dropped in the firewall for every proxy,
        whatever this policy says.
      </span>

      {!allowAll ? (
        <div className="col">
          <ul className="list">
            {ranges.map((r, i) => (
              <li key={`${r.lo}-${r.hi}`} className="list-item" style={{ cursor: 'default' }}>
                <div className="row">
                  <span className="mono">{r.lo === r.hi ? r.lo : `${r.lo}-${r.hi}`}</span>
                  <Button
                    variant="danger"
                    onClick={() => setRanges(ranges.filter((_, j) => j !== i))}
                    aria-label={`Remove port range ${r.lo}-${r.hi}`}
                  >
                    Remove
                  </Button>
                </div>
              </li>
            ))}
          </ul>
          <div className="row">
            <Input label="From port" value={lo} inputMode="numeric" onChange={(e) => setLo(e.target.value)} />
            <Input
              label="To port"
              value={hi}
              inputMode="numeric"
              placeholder="same as from"
              onChange={(e) => setHi(e.target.value)}
            />
            <Button
              disabled={!addable}
              onClick={() => {
                setRanges([...ranges, { lo: parsedLo, hi: parsedHi }])
                setLo('')
                setHi('')
              }}
            >
              Add range
            </Button>
          </div>
          {invalid ? (
            <Notice tone="danger" title="An empty allow-list blocks everything">
              Add at least one range, or switch back to allow-all.
            </Notice>
          ) : null}
        </div>
      ) : null}

      <div className="row">
        <Input
          label="Max connections"
          value={maxConn}
          inputMode="numeric"
          onChange={(e) => setMaxConn(e.target.value)}
        />
        <Input
          label="Per-client limit"
          value={connLimit}
          inputMode="numeric"
          onChange={(e) => setConnLimit(e.target.value)}
        />
      </div>

      {save.isError ? (
        <Notice tone="danger" title="Could not save the port policy">
          {save.error.message}
        </Notice>
      ) : null}
      {save.isSuccess ? <Notice tone="ok" title="Port policy accepted" /> : null}

      <div className="row">
        <Button
          variant="primary"
          disabled={invalid}
          busy={save.isPending}
          onClick={() => save.mutate({ proxyId: proxy.id, policy })}
        >
          Save ports
        </Button>
      </div>
    </section>
  )
}
