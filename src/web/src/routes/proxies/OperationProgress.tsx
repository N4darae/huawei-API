import { useRef } from 'react'
import type { ReactNode } from 'react'
import { Notice, Steps } from '../../design'
import type { StepItem, StepPhase } from '../../design'
import { ROTATE_STEPS } from '../../api/keys'
import type { Op } from '../../api/keys'
import { isTerminalOp, rotateOutcome } from '../../api/query'
import { formatDurationMs } from './format'

export function isSimPinLocked(op: Op | undefined): boolean {
  if (!op) return false
  const bag = (op.result ?? {}) as Record<string, unknown>
  const code = typeof bag['error'] === 'string' ? (bag['error'] as string) : ''
  return /sim[_ -]?p(i|u)[nk]/i.test(`${op.error ?? ''} ${code}`)
}

export const SIM_PIN_COPY = 'SIM is PIN-locked — unlock it in a phone and re-plug'

function useStepTrail(op: Op | undefined): string[] {
  const ref = useRef<{ id: string; steps: string[] }>({ id: '', steps: [] })
  if (op) {
    if (ref.current.id !== op.id) ref.current = { id: op.id, steps: [] }
    if (op.step && !ref.current.steps.includes(op.step)) {
      ref.current.steps = [...ref.current.steps, op.step]
    }
  }
  return op ? ref.current.steps : []
}

function phaseFor(index: number, currentIndex: number, op: Op, stalled: boolean): StepPhase {
  if (index < currentIndex) return 'done'
  if (index > currentIndex) return 'pending'
  if (stalled) return 'stalled'
  if (op.state === 'failed' || op.state === 'canceled') return 'failed'
  if (isTerminalOp(op)) return 'done'
  return 'current'
}

export interface OperationProgressProps {
  op: Op | undefined
  stalled: boolean
  stepNotes?: Record<string, ReactNode>
  label?: string
}

export function OperationProgress({ op, stalled, stepNotes, label }: OperationProgressProps) {
  const trail = useStepTrail(op)
  if (!op) return null

  const frozen = op.kind === 'rotate'
  const names: string[] = frozen ? [...ROTATE_STEPS] : trail
  const known = names.includes(op.step)
  const all = known ? names : [...names, op.step]
  const currentIndex = all.indexOf(op.step)

  const items: StepItem[] = all.map((name, i) => ({
    name,
    phase: phaseFor(i, currentIndex, op, stalled),
    note: stepNotes?.[name],
  }))

  return (
    <div className="col">
      <div className="row">
        <span className="mono muted">{op.id}</span>
        <span className="grow" />
        <span className="muted">
          {op.state} · {op.pct}%
        </span>
      </div>
      <Steps label={label ?? `${op.kind} progress`} items={items} />
      {stalled ? (
        <Notice tone="warn" title="Stalled">
          No progress reported since the deadline at {new Date(op.deadline_at).toISOString().slice(11, 19)}Z.
          The operation has not finished and the backend has stopped reporting steps.
        </Notice>
      ) : null}
    </div>
  )
}

export function RotateOutcomeNotice({ op }: { op: Op | undefined }) {
  if (!op) return null
  const outcome = rotateOutcome(op)
  if (outcome.kind === 'pending') return null

  const ips = outcome.oldIp || outcome.newIp ? `${outcome.oldIp ?? '?'} → ${outcome.newIp ?? '?'}` : null
  const took = outcome.durationMs != null ? ` · took ${formatDurationMs(outcome.durationMs)}` : ''

  if (outcome.kind === 'changed') {
    return (
      <Notice tone="ok" title="Rotated — public IP changed">
        {ips}
        {took}
      </Notice>
    )
  }

  if (outcome.kind === 'unchanged') {
    return (
      <Notice tone="danger" title="Rotation FAILED — the public IP did not change">
        The carrier handed back the same address{ips ? ` (${ips})` : ''}. This is a failure, not a
        success; the customer still has the old IP.{took}
      </Notice>
    )
  }

  if (outcome.kind === 'canceled') {
    return <Notice tone="warn" title="Rotation canceled">{outcome.error}</Notice>
  }

  if (outcome.kind === 'unknown') {
    return (
      <Notice tone="warn" title="Finished without a reported result">
        The operation ended but the backend did not report whether the IP changed. Treat this as
        unverified and re-check the WAN IP.
      </Notice>
    )
  }

  return (
    <Notice tone="danger" title="Rotation failed">
      {isSimPinLocked(op) ? SIM_PIN_COPY : (outcome.error ?? 'no error detail reported')}
    </Notice>
  )
}
