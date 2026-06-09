import { useEffect, useState } from 'react'
import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query'
import type { components } from './schema'
import { isStalled, qk } from './keys'
import type {
  ApiKey,
  Dongle,
  DongleDetail,
  Op,
  Proxy,
  ProxyDetail,
  ProxyPolicy,
  ProxyQuery,
  SelftestResult,
  SmsQuery,
} from './keys'
import { ApiFailure, apiJson, apiText, apiVoid, url } from './client'
import { useIsLive } from './sse'

type AuthIPList = components['schemas']['AuthIPList']
type ApiKeyCreated = components['schemas']['ApiKeyCreated']
type ApiKeyRequest = components['schemas']['ApiKeyRequest']
type ApiKeyList = components['schemas']['ApiKeyList']
type DongleList = components['schemas']['DongleList']
type Health = components['schemas']['Health']
type LinkTokenCreated = components['schemas']['LinkTokenCreated']
type NetMode = components['schemas']['NetMode']
type OperationAccepted = components['schemas']['OperationAccepted']
type ProxyList = components['schemas']['ProxyList']
type RotationList = components['schemas']['RotationList']
type Session = components['schemas']['Session']
type SetAuthRequest = components['schemas']['SetAuthRequest']
type SlotList = components['schemas']['SlotList']
type SmsList = components['schemas']['SmsList']

export const POLL_MS = {
  operation: 1500,
  listLive: 30000,
  listBlind: 8000,
  deviceBusy: 1000,
}

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 3000,
        gcTime: 300000,
        refetchOnWindowFocus: true,
        retry: (count, error) => {
          if (error instanceof ApiFailure && error.status >= 400 && error.status < 500) return false
          return count < 2
        },
      },
      mutations: { retry: false },
    },
  })
}

export function useNow(intervalMs = 1000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const h = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(h)
  }, [intervalMs])
  return now
}

function useListInterval(): number {
  return useIsLive() ? POLL_MS.listLive : POLL_MS.listBlind
}

export const TERMINAL_OP_STATES = ['succeeded', 'failed', 'canceled'] as const

export function isTerminalOp(op: Op): boolean {
  return op.finished_at != null || (TERMINAL_OP_STATES as readonly string[]).includes(op.state)
}

export function opStalled(op: Op, now: number): boolean {
  return !isTerminalOp(op) && isStalled(op, now)
}

export type RotateOutcomeKind = 'pending' | 'changed' | 'unchanged' | 'failed' | 'canceled' | 'unknown'

export interface RotateOutcome {
  kind: RotateOutcomeKind
  oldIp?: string
  newIp?: string
  durationMs?: number
  error?: string
}

function pickString(bag: Record<string, unknown>, ...names: string[]): string | undefined {
  for (const n of names) {
    const v = bag[n]
    if (typeof v === 'string' && v !== '') return v
  }
  return undefined
}

export function rotateOutcome(op: Op): RotateOutcome {
  if (!isTerminalOp(op)) return { kind: 'pending' }
  const bag = (op.result ?? {}) as Record<string, unknown>
  const result = typeof bag['result'] === 'string' ? (bag['result'] as string) : undefined
  const ipChanged = typeof bag['ip_changed'] === 'boolean' ? (bag['ip_changed'] as boolean) : undefined
  const oldIp = pickString(bag, 'old_ip', 'old_public_ip')
  const newIp = pickString(bag, 'new_ip', 'new_public_ip')
  const durationMs = typeof bag['duration_ms'] === 'number' ? (bag['duration_ms'] as number) : undefined
  const error = op.error && op.error !== '' ? op.error : pickString(bag, 'error')
  const base = { oldIp, newIp, durationMs, error }

  if (op.state === 'canceled') return { ...base, kind: 'canceled' }
  if (op.state === 'failed' || result === 'failed') return { ...base, kind: 'failed' }
  if (result === 'unchanged' || ipChanged === false) return { ...base, kind: 'unchanged' }
  if (result === 'changed' || ipChanged === true) return { ...base, kind: 'changed' }
  return { ...base, kind: 'unknown' }
}

export function useHealth(): UseQueryResult<Health, ApiFailure> {
  return useQuery<Health, ApiFailure>({
    queryKey: qk.health(),
    queryFn: ({ signal }) => apiJson<Health>('GET', url.healthz(), { signal }),
    refetchInterval: 30000,
  })
}

export function useSession(): UseQueryResult<Session | null, ApiFailure> {
  return useQuery<Session | null, ApiFailure>({
    queryKey: qk.session(),
    retry: false,
    queryFn: async ({ signal }) => {
      try {
        return await apiJson<Session>('GET', url.session(), { signal })
      } catch (err) {
        if (err instanceof ApiFailure && err.isUnauthorized) return null
        throw err
      }
    },
  })
}

export function useLogin(): UseMutationResult<void, ApiFailure, { username: string; password: string }> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, { username: string; password: string }>({
    mutationFn: (body) => apiVoid('POST', url.login(), { body }),
    onSuccess: () => qc.invalidateQueries(),
  })
}

export function useLogout(): UseMutationResult<void, ApiFailure, void> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, void>({
    mutationFn: () => apiVoid('POST', url.logout()),
    onSuccess: () => {
      qc.clear()
      void qc.invalidateQueries()
    },
  })
}

export function useProxies(q: ProxyQuery = {}): UseQueryResult<ProxyList, ApiFailure> {
  const refetchInterval = useListInterval()
  return useQuery<ProxyList, ApiFailure>({
    queryKey: qk.proxies(q),
    queryFn: ({ signal }) => apiJson<ProxyList>('GET', url.proxies(), { query: { ...q }, signal }),
    refetchInterval,
  })
}

export function useProxy(id: string | null): UseQueryResult<ProxyDetail, ApiFailure> {
  return useQuery<ProxyDetail, ApiFailure>({
    queryKey: qk.proxy(id ?? ''),
    queryFn: ({ signal }) => apiJson<ProxyDetail>('GET', url.proxy(id as string), { signal }),
    enabled: id != null,
  })
}

export function useProxyAuthIPs(id: string | null): UseQueryResult<AuthIPList, ApiFailure> {
  return useQuery<AuthIPList, ApiFailure>({
    queryKey: qk.proxyAuthIPs(id ?? ''),
    queryFn: ({ signal }) => apiJson<AuthIPList>('GET', url.proxyAuthIPs(id as string), { signal }),
    enabled: id != null,
  })
}

export function useSlots(): UseQueryResult<SlotList, ApiFailure> {
  return useQuery<SlotList, ApiFailure>({
    queryKey: qk.slots(),
    queryFn: ({ signal }) => apiJson<SlotList>('GET', url.slots(), { signal }),
  })
}

export function useDongles(): UseQueryResult<DongleList, ApiFailure> {
  const refetchInterval = useListInterval()
  return useQuery<DongleList, ApiFailure>({
    queryKey: qk.dongles(),
    queryFn: ({ signal }) => apiJson<DongleList>('GET', url.dongles(), { signal }),
    refetchInterval,
  })
}

export function useDongle(id: string | null, busy = false): UseQueryResult<DongleDetail, ApiFailure> {
  const listInterval = useListInterval()
  return useQuery<DongleDetail, ApiFailure>({
    queryKey: qk.dongle(id ?? ''),
    queryFn: ({ signal }) => apiJson<DongleDetail>('GET', url.dongle(id as string), { signal }),
    enabled: id != null,
    retry: false,
    refetchInterval: busy ? POLL_MS.deviceBusy : listInterval,
  })
}

export function useSms(id: string | null, q: SmsQuery = {}): UseQueryResult<SmsList, ApiFailure> {
  return useQuery<SmsList, ApiFailure>({
    queryKey: qk.sms(id ?? '', q),
    queryFn: ({ signal }) => apiJson<SmsList>('GET', url.dongleSms(id as string), { query: { ...q }, signal }),
    enabled: id != null,
  })
}

export function useOperation(id: string | null): UseQueryResult<Op, ApiFailure> {
  return useQuery<Op, ApiFailure>({
    queryKey: qk.operation(id ?? ''),
    queryFn: ({ signal }) => apiJson<Op>('GET', url.operation(id as string), { signal }),
    enabled: id != null,
    staleTime: 0,
    refetchInterval: (query) => {
      const op = query.state.data
      if (op && isTerminalOp(op)) return false
      return POLL_MS.operation
    },
  })
}

export function useOp(id: string | null): { op?: Op; stalled: boolean; loading: boolean } {
  const q = useOperation(id)
  const now = useNow(1000)
  const op = q.data
  return { op, stalled: op ? opStalled(op, now) : false, loading: q.isPending && id != null }
}

export function useRotations(proxyId?: string): UseQueryResult<RotationList, ApiFailure> {
  return useQuery<RotationList, ApiFailure>({
    queryKey: qk.rotations(proxyId),
    queryFn: ({ signal }) =>
      apiJson<RotationList>('GET', url.rotations(), { query: { proxy_id: proxyId, limit: 20 }, signal }),
  })
}

export interface OpStart {
  operationId: string
  attached: boolean
}

async function startOp(path: string, body?: unknown): Promise<OpStart> {
  try {
    const res = await apiJson<OperationAccepted>('POST', path, body === undefined ? undefined : { body })
    return { operationId: res.operation_id, attached: false }
  } catch (err) {
    if (err instanceof ApiFailure && err.isOpInProgress && err.operationId) {
      return { operationId: err.operationId, attached: true }
    }
    throw err
  }
}

export function useRotateProxy(): UseMutationResult<OpStart, ApiFailure, string> {
  return useMutation<OpStart, ApiFailure, string>({
    mutationFn: (proxyId) => startOp(url.proxyRotate(proxyId)),
  })
}

export function useSetProxyAuth(): UseMutationResult<
  OpStart,
  ApiFailure,
  { proxyId: string; body: SetAuthRequest }
> {
  const qc = useQueryClient()
  return useMutation<OpStart, ApiFailure, { proxyId: string; body: SetAuthRequest }>({
    mutationFn: ({ proxyId, body }) => startOp(url.proxyAuth(proxyId), body),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: qk.proxy(v.proxyId) })
      void qc.invalidateQueries({ queryKey: ['proxies'] })
    },
  })
}

export function useAddAuthIP(): UseMutationResult<
  AuthIPList,
  ApiFailure,
  { proxyId: string; cidr: string; note?: string }
> {
  const qc = useQueryClient()
  return useMutation<AuthIPList, ApiFailure, { proxyId: string; cidr: string; note?: string }>({
    mutationFn: ({ proxyId, cidr, note }) =>
      apiJson<AuthIPList>('POST', url.proxyAuthIPs(proxyId), { body: { cidr, note } }),
    onSuccess: (data, v) => {
      qc.setQueryData(qk.proxyAuthIPs(v.proxyId), data)
      void qc.invalidateQueries({ queryKey: qk.proxy(v.proxyId) })
    },
  })
}

export function useDeleteAuthIP(): UseMutationResult<
  AuthIPList,
  ApiFailure,
  { proxyId: string; cidr: string }
> {
  const qc = useQueryClient()
  return useMutation<AuthIPList, ApiFailure, { proxyId: string; cidr: string }>({
    mutationFn: ({ proxyId, cidr }) =>
      apiJson<AuthIPList>('DELETE', url.proxyAuthIPs(proxyId), { body: { cidr } }),
    onSuccess: (data, v) => {
      qc.setQueryData(qk.proxyAuthIPs(v.proxyId), data)
      void qc.invalidateQueries({ queryKey: qk.proxy(v.proxyId) })
    },
  })
}

export function useSetProxyPorts(): UseMutationResult<
  OpStart,
  ApiFailure,
  { proxyId: string; policy: ProxyPolicy }
> {
  const qc = useQueryClient()
  return useMutation<OpStart, ApiFailure, { proxyId: string; policy: ProxyPolicy }>({
    mutationFn: ({ proxyId, policy }) => startOp(url.proxyPorts(proxyId), policy),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: qk.proxy(v.proxyId) })
      void qc.invalidateQueries({ queryKey: ['proxies'] })
    },
  })
}

export function useSelftest(): UseMutationResult<SelftestResult, ApiFailure, string> {
  return useMutation<SelftestResult, ApiFailure, string>({
    mutationFn: (proxyId) => apiJson<SelftestResult>('POST', url.proxySelftest(proxyId)),
  })
}

export function useRebootDongle(): UseMutationResult<OpStart, ApiFailure, string> {
  return useMutation<OpStart, ApiFailure, string>({
    mutationFn: (dongleId) => startOp(url.dongleReboot(dongleId)),
  })
}

export function useSetNetMode(): UseMutationResult<
  OpStart,
  ApiFailure,
  { dongleId: string; netMode: NetMode }
> {
  return useMutation<OpStart, ApiFailure, { dongleId: string; netMode: NetMode }>({
    mutationFn: ({ dongleId, netMode }) => startOp(url.dongleNetMode(dongleId), { net_mode: netMode }),
  })
}

export function useSetLanIP(): UseMutationResult<
  OpStart,
  ApiFailure,
  { dongleId: string; gateway: string }
> {
  return useMutation<OpStart, ApiFailure, { dongleId: string; gateway: string }>({
    mutationFn: ({ dongleId, gateway }) => startOp(url.dongleLanIP(dongleId), { gateway }),
  })
}

export function useSendSms(): UseMutationResult<
  void,
  ApiFailure,
  { dongleId: string; to: string[]; body: string }
> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, { dongleId: string; to: string[]; body: string }>({
    mutationFn: ({ dongleId, to, body }) => apiVoid('POST', url.dongleSmsSend(dongleId), { body: { to, body } }),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: ['dongle', v.dongleId, 'sms'] })
    },
  })
}

export function useDeleteSms(): UseMutationResult<void, ApiFailure, { dongleId: string; index: number }> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, { dongleId: string; index: number }>({
    mutationFn: ({ dongleId, index }) => apiVoid('POST', url.dongleSmsDelete(dongleId), { body: { index } }),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: ['dongle', v.dongleId, 'sms'] })
    },
  })
}

export function useMarkSmsRead(): UseMutationResult<void, ApiFailure, { dongleId: string; index: number }> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, { dongleId: string; index: number }>({
    mutationFn: ({ dongleId, index }) => apiVoid('POST', url.dongleSmsRead(dongleId), { body: { index } }),
    onSuccess: (_r, v) => {
      void qc.invalidateQueries({ queryKey: ['dongle', v.dongleId, 'sms'] })
    },
  })
}

export function useApiKeys(): UseQueryResult<ApiKeyList, ApiFailure> {
  return useQuery<ApiKeyList, ApiFailure>({
    queryKey: qk.keys(),
    queryFn: ({ signal }) => apiJson<ApiKeyList>('GET', url.keys(), { signal }),
  })
}

export function useCreateApiKey(): UseMutationResult<ApiKeyCreated, ApiFailure, ApiKeyRequest> {
  const qc = useQueryClient()
  return useMutation<ApiKeyCreated, ApiFailure, ApiKeyRequest>({
    mutationFn: (body) => apiJson<ApiKeyCreated>('POST', url.keys(), { body }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.keys() }),
  })
}

export function useRevokeApiKey(): UseMutationResult<void, ApiFailure, string> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, string>({
    mutationFn: (keyId) => apiVoid('DELETE', url.key(keyId)),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.keys() }),
  })
}

export function useCreateLinkToken(): UseMutationResult<LinkTokenCreated, ApiFailure, string> {
  const qc = useQueryClient()
  return useMutation<LinkTokenCreated, ApiFailure, string>({
    mutationFn: (keyId) => apiJson<LinkTokenCreated>('POST', url.keyLinkTokens(keyId)),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.keys() }),
  })
}

export function useRevokeLinkToken(): UseMutationResult<void, ApiFailure, string> {
  const qc = useQueryClient()
  return useMutation<void, ApiFailure, string>({
    mutationFn: (tokenId) => apiVoid('DELETE', url.linkToken(tokenId)),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.keys() }),
  })
}

export function fetchExport(params: { format: 'txt' | 'csv'; scheme: 'socks5' | 'http'; ids?: string }) {
  return apiText(url.proxiesExport(), {
    query: { format: params.format, scheme: params.scheme, ids: params.ids },
    accept: params.format === 'csv' ? 'text/csv' : 'text/plain',
  })
}

export type { ApiKey, Dongle, Op, Proxy }
