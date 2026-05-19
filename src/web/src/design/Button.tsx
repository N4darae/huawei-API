import type { ButtonHTMLAttributes, ReactNode } from 'react'

export type ButtonVariant = 'default' | 'primary' | 'danger' | 'ghost'

export interface ButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  variant?: ButtonVariant
  size?: 'sm' | 'md'
  busy?: boolean
  children: ReactNode
}

const VARIANT_CLASS: Record<ButtonVariant, string> = {
  default: '',
  primary: ' btn-primary',
  danger: ' btn-danger',
  ghost: ' btn-ghost',
}

export function Button({
  variant = 'default',
  size = 'sm',
  busy = false,
  disabled,
  className,
  children,
  type,
  ...rest
}: ButtonProps) {
  const cls =
    'btn' + VARIANT_CLASS[variant] + (size === 'md' ? ' btn-md' : '') + (className ? ' ' + className : '')
  return (
    <button
      {...rest}
      type={type ?? 'button'}
      className={cls}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
    >
      {busy ? <span className="spin" aria-hidden="true" /> : null}
      {children}
    </button>
  )
}
