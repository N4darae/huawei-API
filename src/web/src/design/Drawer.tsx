import { useEffect, useId, useRef } from 'react'
import type { ReactNode } from 'react'
import { Button } from './Button'

export interface DrawerProps {
  open: boolean
  title: ReactNode
  onClose: () => void
  headerExtra?: ReactNode
  children: ReactNode
}

export function Drawer({ open, title, onClose, headerExtra, children }: DrawerProps) {
  const panel = useRef<HTMLDivElement | null>(null)
  const titleId = useId()

  useEffect(() => {
    if (!open) return
    const previous = document.activeElement as HTMLElement | null
    panel.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      previous?.focus?.()
    }
  }, [open, onClose])

  if (!open) return null

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <div
        className="drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        ref={panel}
      >
        <div className="drawer-head">
          <h2 className="drawer-title" id={titleId}>
            {title}
          </h2>
          <div className="grow row">{headerExtra}</div>
          <Button variant="ghost" onClick={onClose} aria-label="Close panel">
            Close
          </Button>
        </div>
        <div className="drawer-body">{children}</div>
      </div>
    </>
  )
}
