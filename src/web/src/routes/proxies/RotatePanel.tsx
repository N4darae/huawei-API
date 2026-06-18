import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Button, Notice } from '../../design'
import { qk } from '../../api/keys'
import type { Proxy } from '../../api/keys'
import { isTerminalOp, useNow, useOp, useRotateProxy } from '../../api/query'
import { OperationProgress, RotateOutcomeNotice, SIM_PIN_COPY, isSimPinLocked } from './OperationProgress'
import { formatSeconds } from './format'

export interface RotatePanelProps {
  proxy: Proxy
  autoStart?: boolean
}

export function RotatePanel({ proxy, autoStart = false }: RotatePanelProps) {
  const qc = useQueryClient()
  const rotate = useRotateProxy()
  const [opId, setOpId] = useState<string | null>(proxy.active_operation_id ?? null)
  const [attached, setAttached] = useState(false)
  const [retryAt, setRetryAt] = useState<number | null>(null)
  const { op, stalled } = useOp(opId)
  const now = useNow(500)

  useEffect(() => {
    if (proxy.active_operation_id && proxy.active_operation_id !== opId) {
      setOpId(proxy.active_operation_id)
    }
  }, [proxy.active_operation_id, opId])

  const finished = op?.finished_at ?? null
  useEffect(() => {
    if (finished == null) return
    void qc.invalidateQueries({ queryKey: ['proxies'] })
    void qc.invalidateQueries({ queryKey: qk.proxy(proxy.id) })
    void qc.invalidateQueries({ queryKey: ['rotations'] })
  }, [finished, qc, proxy.id])

  const running = op ? !isTerminalOp(op) : rotate.isPending
  const waitS = retryAt != null ? (retryAt - now) / 1000 : 0
  const throttled = waitS > 0

  const onRotate = useCallback(() => {
    setAttached(false)
    setRetryAt(null)
    rotate.mutate(proxy.id, {
      onSuccess: (r) => {
        setOpId(r.operationId)
        setAttached(r.attached)
      },
      onError: (e) => {
        if (e.isRateLimited) setRetryAt(Date.now() + (e.retryAfterS ?? 60) * 1000)
      },
    })
  }, [rotate, proxy.id])

  const autoStarted = useRef(false)
  useEffect(() => {
    if (!autoStart || autoStarted.current) return
    autoStarted.current = true
    onRotate()
  }, [autoStart, onRotate])

  const failedToStart = rotate.isError && !rotate.error.isRateLimited && !rotate.error.isOpInProgress

  return (
    <section className="card">
      <h3 className="card-title">Rotate</h3>
      <div className="row">
        <Button
          variant="primary"
          size="md"
          busy={running || rotate.isPending}
          disabled={throttled}
          onClick={onRotate}
        >
          {running ? 'Rotating…' : 'Rotate IP'}
        </Button>
        <span className="muted">
          {running
            ? 'The panel waits for the backend to confirm. 30–90s is normal.'
            : 'Takes 30–90 seconds. The result is only shown once the backend confirms it.'}
        </span>
      </div>

      {throttled ? (
        <Notice tone="warn" title="Too soon — per-proxy minimum interval">
          This proxy was rotated recently. Wait {formatSeconds(waitS)} before rotating again.
        </Notice>
      ) : null}

      {attached && running ? (
        <Notice tone="info" title="Already running — attached">
          A rotation was already in progress on this proxy. The panel attached to it instead of
          starting a second one.
        </Notice>
      ) : null}

      {failedToStart ? (
        <Notice tone="danger" title="Could not start the rotation">
          {rotate.error.isSimPinLocked ? SIM_PIN_COPY : rotate.error.message}
        </Notice>
      ) : null}

      <OperationProgress op={op} stalled={stalled} label="Rotate progress" />
      <RotateOutcomeNotice op={op} />

      {op && isSimPinLocked(op) && op.state !== 'failed' ? (
        <Notice tone="danger" title={SIM_PIN_COPY} />
      ) : null}
    </section>
  )
}
