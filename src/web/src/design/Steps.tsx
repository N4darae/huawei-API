import type { ReactNode } from 'react'

export type StepPhase = 'done' | 'current' | 'pending' | 'stalled' | 'failed'

const MARK: Record<StepPhase, string> = {
  done: '✓',
  current: '→',
  pending: '·',
  stalled: '!',
  failed: '✕',
}

const WORD: Record<StepPhase, string> = {
  done: 'done',
  current: 'running',
  pending: 'waiting',
  stalled: 'stalled',
  failed: 'failed',
}

export interface StepItem {
  name: string
  phase: StepPhase
  note?: ReactNode
}

export interface StepsProps {
  label: string
  items: readonly StepItem[]
}

export function Steps({ label, items }: StepsProps) {
  return (
    <ol className="steps" aria-label={label}>
      {items.map((s) => (
        <li key={s.name} className="step" data-phase={s.phase} data-step={s.name}>
          <span aria-hidden="true" style={{ width: 12, display: 'inline-block' }}>
            {MARK[s.phase]}
          </span>
          <span className="step-name">{s.name}</span>
          <span className="grow" />
          {s.note ? <span className="faint">{s.note}</span> : null}
          <span className={s.phase === 'stalled' || s.phase === 'failed' ? undefined : 'faint'}>
            {WORD[s.phase]}
          </span>
        </li>
      ))}
    </ol>
  )
}
