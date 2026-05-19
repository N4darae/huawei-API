import { createContext, useCallback, useContext, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import type { Tone } from './Badge'

export interface ToastInput {
  tone: Tone
  title: string
  detail?: string
  ttlMs?: number
}

interface ToastItem extends ToastInput {
  id: number
}

interface ToastApi {
  push: (t: ToastInput) => void
  dismiss: (id: number) => void
}

const Ctx = createContext<ToastApi | null>(null)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([])
  const seq = useRef(0)

  const dismiss = useCallback((id: number) => {
    setItems((prev) => prev.filter((t) => t.id !== id))
  }, [])

  const push = useCallback(
    (t: ToastInput) => {
      const id = ++seq.current
      setItems((prev) => [...prev.slice(-4), { ...t, id }])
      const ttl = t.ttlMs ?? (t.tone === 'danger' ? 12000 : 6000)
      if (ttl > 0) setTimeout(() => dismiss(id), ttl)
    },
    [dismiss],
  )

  const api = useMemo(() => ({ push, dismiss }), [push, dismiss])

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="toast-stack" role="region" aria-label="Notifications">
        {items.map((t) => (
          <div key={t.id} className="toast" data-tone={t.tone} role={t.tone === 'danger' ? 'alert' : 'status'}>
            <div className="grow col" style={{ gap: 2 }}>
              <span className="toast-title">{t.title}</span>
              {t.detail ? <span className="toast-detail">{t.detail}</span> : null}
            </div>
            <button className="btn btn-ghost" onClick={() => dismiss(t.id)} aria-label="Dismiss notification">
              Dismiss
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export function useToast(): ToastApi {
  const api = useContext(Ctx)
  if (!api) throw new Error('useToast requires ToastProvider')
  return api
}
