import { useCallback, useEffect, useRef, useState } from 'react'
import { Button } from './Button'

export async function writeClipboard(text: string): Promise<boolean> {
  const nav = globalThis.navigator as Navigator | undefined
  if (nav?.clipboard?.writeText) {
    try {
      await nav.clipboard.writeText(text)
      return true
    } catch {
      return false
    }
  }
  return false
}

export interface CopyFieldProps {
  label: string
  value: string
  monospace?: boolean
}

export function CopyField({ label, value, monospace = true }: CopyFieldProps) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  const onCopy = useCallback(() => {
    void writeClipboard(value).then((ok) => {
      setCopied(ok)
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 1800)
    })
  }, [value])

  return (
    <div className="field">
      <span className="field-label">{label}</span>
      <div className="copy">
        <code className={monospace ? 'mono' : undefined}>{value}</code>
        <Button onClick={onCopy} aria-label={`Copy ${label}`}>
          {copied ? 'Copied' : 'Copy'}
        </Button>
      </div>
    </div>
  )
}
