import { useState } from 'react'
import { Button, Notice } from '../../design'
import type { Dongle } from '../../api/keys'
import { isTerminalOp, useOp, useRebootDongle } from '../../api/query'
import { OperationProgress } from '../proxies/OperationProgress'

export function RebootPanel({ dongle }: { dongle: Dongle }) {
  const [confirming, setConfirming] = useState(false)
  const [opId, setOpId] = useState<string | null>(null)
  const reboot = useRebootDongle()
  const { op, stalled } = useOp(opId)
  const running = opId != null && (!op || !isTerminalOp(op))

  return (
    <section className="card">
      <h3 className="card-title">Reboot</h3>
      <div className="row">
        {!confirming ? (
          <Button variant="danger" disabled={running} onClick={() => setConfirming(true)}>
            Reboot dongle
          </Button>
        ) : null}
        <span className="muted">The stick disappears from USB for about 40 seconds.</span>
      </div>

      {confirming ? (
        <>
          <Notice tone="warn" title="This will drop the connection">
            Every session through this dongle ends immediately and the proxy is down until the stick
            re-enumerates.
          </Notice>
          <div className="row">
            <Button
              variant="danger"
              busy={reboot.isPending}
              onClick={() => {
                setConfirming(false)
                reboot.mutate(dongle.id, { onSuccess: (r) => setOpId(r.operationId) })
              }}
            >
              Yes, reboot {dongle.id}
            </Button>
            <Button onClick={() => setConfirming(false)}>Cancel</Button>
          </div>
        </>
      ) : null}

      {reboot.isError ? (
        <Notice tone="danger" title="Could not start the reboot">
          {reboot.error.message}
        </Notice>
      ) : null}

      <OperationProgress op={op} stalled={stalled} label="Reboot progress" />

      {op && isTerminalOp(op) && op.state === 'succeeded' ? (
        <Notice tone="ok" title="Dongle came back" />
      ) : null}
    </section>
  )
}
