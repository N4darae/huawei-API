import { useState } from 'react'
import { Button, CopyField, Input, Notice } from '../../design'
import type { Dongle, Op, Slot } from '../../api/keys'
import { isTerminalOp, useSetLanIP } from '../../api/query'
import { OperationProgress } from '../proxies/OperationProgress'

export const RE_DISCOVERING_STEP = 're_discovering'

export const RE_DISCOVERING_COPY =
  'The old address stops answering while the modem moves. That silence is the operation working, not a failure.'

const IPV4 = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/

export function isIPv4(v: string): boolean {
  const m = IPV4.exec(v.trim())
  if (!m) return false
  return m.slice(1).every((o) => {
    const n = Number(o)
    return n >= 0 && n <= 255
  })
}

export interface LanIpPanelProps {
  dongle: Dongle
  slot: Slot | undefined
  op: Op | undefined
  stalled: boolean
  running: boolean
  onStart: (opId: string) => void
}

export function LanIpPanel({ dongle, slot, op, stalled, running, onStart }: LanIpPanelProps) {
  const [gateway, setGateway] = useState(slot?.gateway_ip ?? '')
  const [confirming, setConfirming] = useState(false)
  const setLanIP = useSetLanIP()
  const valid = isIPv4(gateway)
  const supported = dongle.lan_ip_change_supported !== false

  if (!supported) {
    const path = slot?.usb_path ?? ''
    return (
      <section className="card">
        <h3 className="card-title">LAN address</h3>
        <Notice tone="warn" title="This firmware does not accept a LAN address change over HiLink">
          Re-seat the stick by hand. Run this on the farm host, then re-enroll the slot:
        </Notice>
        <CopyField
          label="Manual reset command"
          value={path ? `usbreset ${path}` : 'usbreset <usb path unknown — check the slot record>'}
        />
      </section>
    )
  }

  return (
    <section className="card">
      <h3 className="card-title">LAN address</h3>
      <div className="row">
        <span className="muted">Slot gateway:</span>
        <span className="mono">{slot?.gateway_ip ?? 'unknown'}</span>
        <span className="muted">host:</span>
        <span className="mono">{slot?.host_ip ?? 'unknown'}</span>
      </div>

      <div className="row">
        <Input
          label="New gateway address"
          value={gateway}
          onChange={(e) => setGateway(e.target.value)}
          placeholder="192.168.101.1"
          disabled={running}
          error={gateway !== '' && !valid ? 'Not an IPv4 address' : undefined}
        />
        {!confirming ? (
          <Button variant="primary" disabled={!valid || running} onClick={() => setConfirming(true)}>
            Change LAN address
          </Button>
        ) : null}
      </div>

      {confirming ? (
        <>
          <Notice tone="warn" title="The dongle goes quiet during this change">
            {RE_DISCOVERING_COPY} The panel re-discovers it on the new address and only reports a
            failure if the deadline passes.
          </Notice>
          <div className="row">
            <Button
              variant="danger"
              busy={setLanIP.isPending}
              onClick={() => {
                setConfirming(false)
                setLanIP.mutate(
                  { dongleId: dongle.id, gateway: gateway.trim() },
                  { onSuccess: (r) => onStart(r.operationId) },
                )
              }}
            >
              Move the dongle to {gateway}
            </Button>
            <Button onClick={() => setConfirming(false)}>Cancel</Button>
          </div>
        </>
      ) : null}

      {setLanIP.isError ? (
        <Notice tone="danger" title="Could not start the address change">
          {setLanIP.error.message}
        </Notice>
      ) : null}

      {running ? (
        <Notice tone="info" title="Re-discovering the dongle">
          {RE_DISCOVERING_COPY}
        </Notice>
      ) : null}

      <OperationProgress
        op={op}
        stalled={stalled}
        label="LAN address progress"
        stepNotes={{ [RE_DISCOVERING_STEP]: 'old address is expected to be silent' }}
      />

      {op && isTerminalOp(op) && op.state === 'succeeded' ? (
        <Notice tone="ok" title="Dongle answered on its new address" />
      ) : null}
    </section>
  )
}
