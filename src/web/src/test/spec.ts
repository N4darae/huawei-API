import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const SPEC_PATH = resolve(dirname(fileURLToPath(import.meta.url)), '../../../api/openapi.yaml')

export type SpecMethod = 'get' | 'post' | 'patch' | 'delete' | 'put'

export interface SpecRoute {
  method: SpecMethod
  path: string
  operationId: string
}

const METHODS: SpecMethod[] = ['get', 'post', 'patch', 'delete', 'put']

export function readSpecRoutes(): SpecRoute[] {
  const lines = readFileSync(SPEC_PATH, 'utf8').split('\n')
  const routes: SpecRoute[] = []
  let inPaths = false
  let path: string | null = null
  let method: SpecMethod | null = null

  for (const line of lines) {
    if (/^paths:\s*$/.test(line)) {
      inPaths = true
      continue
    }
    if (!inPaths) continue
    if (/^\S/.test(line)) break

    const pathMatch = /^ {2}(\/\S*):\s*$/.exec(line)
    if (pathMatch) {
      path = pathMatch[1] ?? null
      method = null
      continue
    }

    const methodMatch = /^ {4}([a-z]+):\s*$/.exec(line)
    if (methodMatch && METHODS.includes(methodMatch[1] as SpecMethod)) {
      method = methodMatch[1] as SpecMethod
      continue
    }

    const opMatch = /^ {6}operationId:\s*(\S+)\s*$/.exec(line)
    if (opMatch && path && method) {
      routes.push({ method, path, operationId: opMatch[1] as string })
      method = null
    }
  }

  return routes
}

export function routeKey(r: Pick<SpecRoute, 'method' | 'path'>): string {
  return `${r.method.toUpperCase()} ${r.path}`
}

export function toMswPath(path: string): string {
  return path.replace(/\{(\w+)\}/g, ':$1')
}
