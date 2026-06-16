import { useState } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { Badge, Button, ToastProvider, applyTheme, readTheme, useToast } from './design'
import type { ThemeChoice } from './design'
import { createQueryClient, useHealth, useLogout, useNow } from './api/query'
import { LiveProvider, useLive } from './api/sse'
import type { Topic } from './api/events'
import { AuthGate } from './auth'
import { Link, ROUTES, useRoutePath, useTitleSync } from './router'
import { ProxiesPage } from './routes/proxies/ProxiesPage'
import { DonglesPage } from './routes/dongles/DonglesPage'
import { KeysPage } from './routes/keys/KeysPage'
import { formatAgo } from './routes/proxies/format'

const TOPICS: Topic[] = ['proxies', 'dongles', 'operations', 'sms', 'system']

function ConnBadge() {
  const live = useLive()
  const now = useNow(2000)
  const age = live.lastEventAt == null ? null : formatAgo(live.lastEventAt, now)

  const label =
    live.status === 'live'
      ? live.stale
        ? 'live stream silent'
        : 'live'
      : live.status === 'reconnecting'
        ? 'stream lost — reconnecting'
        : live.status === 'connecting'
          ? 'stream connecting'
          : 'no stream — polling'

  const tone = live.status === 'live' ? (live.stale ? 'warn' : 'ok') : 'warn'

  return (
    <span className="conn" title={live.nodeId ? `node ${live.nodeId}` : undefined}>
      <Badge tone={tone}>{label}</Badge>
      <span className="faint">{age ? `last event ${age}` : 'no events yet'}</span>
    </span>
  )
}

function HealthBadge() {
  const health = useHealth()
  if (health.isError) return <Badge tone="danger">panel unreachable</Badge>
  const h = health.data
  if (!h) return null
  const bad = h.invariants.filter((i) => !i.ok)
  if (h.status === 'ok' && bad.length === 0) return <Badge tone="ok">invariants ok</Badge>
  return (
    <Badge tone="danger" title={bad.map((i) => `${i.name}: ${i.detail ?? 'failed'}`).join('\n')}>
      {bad.length} invariants failing
    </Badge>
  )
}

function ThemePicker() {
  const [choice, setChoice] = useState<ThemeChoice>(readTheme)
  return (
    <label className="row" style={{ gap: 6 }}>
      <span className="sr-only">Theme</span>
      <select
        className="field-input"
        style={{ width: 'auto' }}
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
    <div className="app">
      <header className="topbar">
        <span className="brand">dongled</span>
        <nav className="nav" aria-label="Main">
          {ROUTES.map((r) => (
            <Link key={r.path} to={r.path}>
              {r.label}
            </Link>
          ))}
        </nav>
        <span className="grow" />
        <HealthBadge />
        <ConnBadge />
        <ThemePicker />
        <Button busy={logout.isPending} onClick={() => logout.mutate()}>
          Sign out
        </Button>
      </header>
      {path === '/dongles' ? <DonglesPage /> : path === '/keys' ? <KeysPage /> : <ProxiesPage />}
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
