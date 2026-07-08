import type { ReactNode } from 'react'

export type Tone = 'neutral' | 'ok' | 'warn' | 'danger' | 'info'

export interface BadgeProps {
  tone?: Tone
  title?: string
  children: ReactNode
}

export function Badge({ tone = 'neutral', title, children }: BadgeProps) {
  return (
    <span className={'badge badge-' + tone} title={title}>
      {children}
    </span>
  )
}

export interface DotProps {
  tone?: Tone
  filled: boolean
  label: string
}

export function Dot({ tone = 'neutral', filled, label }: DotProps) {
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={'dot ' + (filled ? 'dot-filled' : 'dot-hollow') + ' dot-' + tone}
    />
  )
}

export interface NoticeProps {
  tone: Tone
  title: ReactNode
  children?: ReactNode
  compact?: boolean
  hint?: string
}

export function Notice({ tone, title, children, compact = false, hint }: NoticeProps) {
  return (
    <div
      className={compact ? 'notice notice-compact' : 'notice'}
      data-tone={tone}
      role={tone === 'danger' ? 'alert' : 'status'}
      title={hint}
    >
      <span className="notice-title">{title}</span>
      {children ? <span>{children}</span> : null}
    </div>
  )
}
