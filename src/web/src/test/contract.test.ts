import { describe, expect, it } from 'vitest'
import { NOT_CALLED_BY_SPA, RESOLVERS, buildHandlers } from './handlers'
import { readSpecRoutes, routeKey } from './spec'

describe('msw handlers are generated from openapi.yaml', () => {
  const routes = readSpecRoutes()
  const specKeys = new Set(routes.map(routeKey))

  it('finds every operation in the spec', () => {
    expect(routes.length).toBeGreaterThan(40)
    expect(specKeys.has('POST /api/v1/proxies/{proxy_id}/rotate')).toBe(true)
    expect(specKeys.has('GET /api/v1/proxies/export')).toBe(true)
  })

  it('has no resolver for a route the contract does not declare', () => {
    const orphans = Object.keys(RESOLVERS).filter((k) => !specKeys.has(k))
    expect(orphans).toEqual([])
  })

  it('accounts for every contract route, mocked or explicitly unused', () => {
    const unaccounted = [...specKeys].filter((k) => !(k in RESOLVERS) && !NOT_CALLED_BY_SPA.has(k))
    expect(unaccounted).toEqual([])
  })

  it('builds one handler per contract route', () => {
    expect(buildHandlers()).toHaveLength(routes.length)
  })
})
