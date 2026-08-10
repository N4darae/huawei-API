import { useState } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Button, Dot, ToastProvider, applyTheme, readTheme, useToast } from './design'
import type { ThemeChoice, Tone } from './design'
import { createQueryClient, useHealth, useLogout, useNow } from './api/query'
import { LiveProvider, useLive } from './api/sse'
import type { LiveStatus } from './api/sse'
import type { Topic } from './api/events'
import { AuthGate } from './auth'
import { Link, ROUTES, useRoutePath, useTitleSync } from './router'
import { ProxiesPage } from './routes/proxies/ProxiesPage'
import { DonglesPage } from './routes/dongles/DonglesPage'
import { KeysPage } from './routes/keys/KeysPage'
import { formatAgo } from './routes/proxies/format'

const TOPICS: Topic[] = ['proxies', 'dongles', 'operations', 'sms', 'system']

function streamTone(status: LiveStatus, stale: boolean): Tone {
  return status === 'live' && !stale ? 'ok' : 'warn'
}

function streamLabel(status: LiveStatus, stale: boolean): string {
  if (status === 'live') return stale ? 'live stream silent' : 'live'
  if (status === 'reconnecting') return 'stream lost — reconnecting'
  if (status === 'connecting') return 'stream connecting'
  return 'no stream — polling'
}

function SidebarHealth() {
  const health = useHealth()
  const live = useLive()
  const now = useNow(2000)

  const bad = health.data?.invariants.filter((i) => !i.ok) ?? []
  const healthOk = !health.isError && health.data?.status === 'ok' && bad.length === 0
  const healthLabel = health.isError
    ? 'panel unreachable'
    : bad.length > 0
      ? `${bad.length} invariants failing`
      : 'invariants ok'

  const streamText = streamLabel(live.status, live.stale)
  const age =
    live.lastEventAt == null ? 'no events yet' : `last event ${formatAgo(live.lastEventAt, now)}`

  const tone: Tone = !healthOk ? 'danger' : streamTone(live.status, live.stale)
  const summary = healthOk ? streamText : healthLabel
  const detail = [healthLabel, streamText, age].join(' · ')

  return (
    <span className="sidebar-health" data-tone={tone} title={detail}>
      <Dot tone={tone} filled label={detail} />
      <span className="sidebar-health-text">{summary}</span>
    </span>
  )
}

function ThemePicker() {
  const [choice, setChoice] = useState<ThemeChoice>(readTheme)
  return (
    <label className="row" style={{ gap: 4 }}>
      <span className="sr-only">Theme</span>
      <select
        className="field-input theme-select"
        value={choice}
        onChange={(e) => {
          const v = e.target.value as ThemeChoice
          setChoice(v)
          applyTheme(v)
        }}
      >
        <option value="system">System theme</option>
        <option value="dark">Dark</option>
        <option value="light">Light</option>
      </select>
    </label>
  )
}

export function Shell() {
  const path = useRoutePath()
  const logout = useLogout()
  useTitleSync(path === '/' ? 'Proxies · dongled' : path === '/dongles' ? 'Dongles · dongled' : 'API keys · dongled')

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <span className="sidebar-mark" aria-hidden="true" />
          dongled
        </div>
        <nav className="sidebar-nav" aria-label="Main">
          {ROUTES.map((r) => (
            <Link key={r.path} to={r.path}>
              {r.label}
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          <SidebarHealth />
          <div className="sidebar-foot-row">
            <ThemePicker />
            <Button variant="ghost" busy={logout.isPending} onClick={() => logout.mutate()}>
              Sign out
            </Button>
          </div>
        </div>
      </aside>
      <main className="main">
        {path === '/dongles' ? <DonglesPage /> : path === '/keys' ? <KeysPage /> : <ProxiesPage />}
      </main>
    </div>
  )
}

function LiveShell({ children }: { children: ReactNode }) {
  const toast = useToast()
  return (
    <LiveProvider
      topics={TOPICS}
      onNotice={(n) =>
        toast.push({
          tone: n.level === 'error' ? 'danger' : n.level === 'warn' ? 'warn' : 'info',
          title: n.title,
          detail: n.detail,
        })
      }
    >
      {children}
    </LiveProvider>
  )
}

export function App() {
  const [client] = useState(createQueryClient)
  return (
    <QueryClientProvider client={client}>
      <ToastProvider>
        <AuthGate>
          <LiveShell>
            <Shell />
          </LiveShell>
        </AuthGate>
      </ToastProvider>
    </QueryClientProvider>
  )
}
