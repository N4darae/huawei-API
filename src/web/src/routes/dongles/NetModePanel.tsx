import { useState } from 'react'
import { Button, Notice, Select } from '../../design'
import type { Dongle } from '../../api/keys'
import { isTerminalOp, useOp, useSetNetMode } from '../../api/query'
import { OperationProgress } from '../proxies/OperationProgress'
import type { components } from '../../api/schema'

type NetMode = components['schemas']['NetMode']

const MODES: Array<{ value: NetMode; label: string }> = [
  { value: 'lte', label: 'LTE only (lock)' },
  { value: '3g', label: '3G only' },
  { value: '2g', label: '2G only' },
  { value: 'auto', label: 'Automatic' },
]

export const NETMODE_WARNING = 'This will drop the connection'

export function NetModePanel({ dongle }: { dongle: Dongle }) {
  const [mode, setMode] = useState<NetMode>('lte')
  const [confirming, setConfirming] = useState(false)
  const [opId, setOpId] = useState<string | null>(null)
  const setNetMode = useSetNetMode()
  const { op, stalled } = useOp(opId)
  const running = opId != null && (!op || !isTerminalOp(op))

  return (
    <section className="card">
      <h3 className="card-title">Network mode</h3>
      <div className="row">
        <span className="muted">Current:</span>
        <span className="mono">{dongle.net_mode ?? 'unknown'}</span>
      </div>

      <div className="row">
        <Select label="Lock to" value={mode} onChange={(e) => setMode(e.target.value as NetMode)}>
          {MODES.map((m) => (
            <option key={m.value} value={m.value}>
              {m.label}
            </option>
          ))}
        </Select>
        {!confirming ? (
          <Button variant="primary" disabled={running} onClick={() => setConfirming(true)}>
            Apply network mode
          </Button>
        ) : null}
      </div>

      {confirming ? (
        <Notice tone="warn" title={`${NETMODE_WARNING} — confirm`}>
          Switching the radio to <span className="mono">{mode}</span> re-attaches the modem. Every
          session through this dongle drops and the WAN IP will change. Customers on this proxy see a
          hard disconnect for roughly 20 seconds.
        </Notice>
      ) : null}

      {confirming ? (
        <div className="row">
          <Button
            variant="danger"
            busy={setNetMode.isPending}
            onClick={() => {
              setConfirming(false)
              setNetMode.mutate(
                { dongleId: dongle.id, netMode: mode },
                { onSuccess: (r) => setOpId(r.operationId) },
              )
            }}
          >
            Yes, drop the connection and switch to {mode}
          </Button>
          <Button onClick={() => setConfirming(false)}>Cancel</Button>
        </div>
      ) : null}

      {setNetMode.isError ? (
        <Notice tone="danger" title="Could not change the network mode">
          {setNetMode.error.message}
        </Notice>
      ) : null}

      <OperationProgress op={op} stalled={stalled} label="Network mode progress" />

      {op && isTerminalOp(op) && op.state === 'succeeded' ? (
        <Notice tone="ok" title={`Radio locked to ${mode}`} />
      ) : null}
    </section>
  )
}
