import { useId } from 'react'
import type { ReactNode } from 'react'

export interface TabDef {
  id: string
  label: string
  panel: ReactNode
}

export interface TabsProps {
  label: string
  tabs: readonly TabDef[]
  active: string
  onChange: (id: string) => void
}

export function Tabs({ label, tabs, active, onChange }: TabsProps) {
  const base = useId()
  const current = tabs.find((t) => t.id === active) ?? tabs[0]
  return (
    <div className="col">
      <div className="tabs" role="tablist" aria-label={label}>
        {tabs.map((t) => (
          <button
            key={t.id}
            className="tab"
            role="tab"
            type="button"
            id={`${base}-${t.id}-tab`}
            aria-selected={current?.id === t.id}
            aria-controls={`${base}-${t.id}-panel`}
            tabIndex={current?.id === t.id ? 0 : -1}
            onClick={() => onChange(t.id)}
            onKeyDown={(e) => {
              const i = tabs.findIndex((x) => x.id === current?.id)
              if (e.key === 'ArrowRight') {
                const next = tabs[(i + 1) % tabs.length]
                if (next) onChange(next.id)
              } else if (e.key === 'ArrowLeft') {
                const prev = tabs[(i - 1 + tabs.length) % tabs.length]
                if (prev) onChange(prev.id)
              }
            }}
          >
            {t.label}
          </button>
        ))}
      </div>
      {current ? (
        <div
          role="tabpanel"
          id={`${base}-${current.id}-panel`}
          aria-labelledby={`${base}-${current.id}-tab`}
          tabIndex={0}
        >
          {current.panel}
        </div>
      ) : null}
    </div>
  )
}
