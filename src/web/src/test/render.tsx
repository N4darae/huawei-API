import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { RenderResult } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { ToastProvider } from '../design'
import { POLL_MS } from '../api/query'

POLL_MS.operation = 15
POLL_MS.listLive = 5_000
POLL_MS.listBlind = 5_000
POLL_MS.deviceBusy = 20

export function testQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: 0, gcTime: 0, refetchOnWindowFocus: false },
      mutations: { retry: false },
    },
  })
}

export interface Harness {
  client: QueryClient
  view: RenderResult
}

export function renderApp(ui: ReactElement, client: QueryClient = testQueryClient()): Harness {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <ToastProvider>{children}</ToastProvider>
    </QueryClientProvider>
  )
  return { client, view: render(ui, { wrapper }) }
}
