import { useId } from 'react'
import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react'

interface FieldShell {
  label: string
  hint?: ReactNode
  error?: ReactNode
}

export type InputProps = FieldShell & Omit<InputHTMLAttributes<HTMLInputElement>, 'id'>

export function Input({ label, hint, error, className, ...rest }: InputProps) {
  const id = useId()
  const describedBy = error ? id + '-err' : hint ? id + '-hint' : undefined
  return (
    <div className={'field' + (className ? ' ' + className : '')}>
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <input
        {...rest}
        id={id}
        className="field-input"
        aria-describedby={describedBy}
        aria-invalid={error ? true : undefined}
      />
      {error ? (
        <span className="field-error" id={id + '-err'}>
          {error}
        </span>
      ) : hint ? (
        <span className="field-hint" id={id + '-hint'}>
          {hint}
        </span>
      ) : null}
    </div>
  )
}

export type TextAreaProps = FieldShell & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'id'>

export function TextArea({ label, hint, error, className, ...rest }: TextAreaProps) {
  const id = useId()
  const describedBy = error ? id + '-err' : hint ? id + '-hint' : undefined
  return (
    <div className={'field' + (className ? ' ' + className : '')}>
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <textarea
        {...rest}
        id={id}
        className="field-input"
        aria-describedby={describedBy}
        aria-invalid={error ? true : undefined}
      />
      {error ? (
        <span className="field-error" id={id + '-err'}>
          {error}
        </span>
      ) : hint ? (
        <span className="field-hint" id={id + '-hint'}>
          {hint}
        </span>
      ) : null}
    </div>
  )
}

export type SelectProps = FieldShell & Omit<SelectHTMLAttributes<HTMLSelectElement>, 'id'> & {
  children: ReactNode
}

export function Select({ label, hint, error, className, children, ...rest }: SelectProps) {
  const id = useId()
  const describedBy = error ? id + '-err' : hint ? id + '-hint' : undefined
  return (
    <div className={'field' + (className ? ' ' + className : '')}>
      <label className="field-label" htmlFor={id}>
        {label}
      </label>
      <select {...rest} id={id} className="field-input" aria-describedby={describedBy}>
        {children}
      </select>
      {error ? (
        <span className="field-error" id={id + '-err'}>
          {error}
        </span>
      ) : hint ? (
        <span className="field-hint" id={id + '-hint'}>
          {hint}
        </span>
      ) : null}
    </div>
  )
}
