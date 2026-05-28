import { useCallback, useEffect, useMemo, useSyncExternalStore } from 'react'
import type { AnchorHTMLAttributes, ReactNode } from 'react'

export interface AppLocation {
  path: string
  search: URLSearchParams
}

const listeners = new Set<() => void>()

function emit(): void {
  for (const fn of listeners) fn()
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  globalThis.addEventListener('popstate', fn)
  return () => {
    listeners.delete(fn)
    globalThis.removeEventListener('popstate', fn)
  }
}

function snapshot(): string {
  const loc = globalThis.location
  return loc ? loc.pathname + loc.search : '/'
}

export function navigate(to: string, replace = false): void {
  const current = snapshot()
  if (to === current) return
  if (replace) globalThis.history.replaceState(null, '', to)
  else globalThis.history.pushState(null, '', to)
  emit()
}

export function useHref(): string {
  return useSyncExternalStore(subscribe, snapshot, () => '/')
}

export function useLocation(): AppLocation {
  const href = useHref()
  return useMemo(() => {
    const q = href.indexOf('?')
    return {
      path: q === -1 ? href : href.slice(0, q),
      search: new URLSearchParams(q === -1 ? '' : href.slice(q + 1)),
    }
  }, [href])
}

export function useSearchParam(name: string): [string | null, (value: string | null) => void] {
  const loc = useLocation()
  const value = loc.search.get(name)
  const set = useCallback(
    (next: string | null) => {
      const params = new URLSearchParams(globalThis.location.search)
      if (next === null) params.delete(name)
      else params.set(name, next)
      const qs = params.toString()
      navigate(globalThis.location.pathname + (qs ? '?' + qs : ''), true)
    },
    [name],
  )
  return [value, set]
}

export interface LinkProps extends Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'href'> {
  to: string
  children: ReactNode
}

export function Link({ to, children, onClick, ...rest }: LinkProps) {
  const loc = useLocation()
  return (
    <a
      {...rest}
      href={to}
      aria-current={loc.path === to ? 'page' : undefined}
      onClick={(e) => {
        onClick?.(e)
        if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
        e.preventDefault()
        navigate(to)
      }}
    >
      {children}
    </a>
  )
}

export const ROUTES = [
  { path: '/', label: 'Proxies' },
  { path: '/dongles', label: 'Dongles' },
  { path: '/keys', label: 'API keys' },
] as const

export type RoutePath = (typeof ROUTES)[number]['path']

export function useRoutePath(): RoutePath {
  const { path } = useLocation()
  if (path === '/dongles') return '/dongles'
  if (path === '/keys') return '/keys'
  return '/'
}

export function useTitleSync(title: string): void {
  useEffect(() => {
    if (globalThis.document) globalThis.document.title = title
  }, [title])
}
