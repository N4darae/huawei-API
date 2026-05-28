import { Badge, CopyField, Drawer, Notice } from '../../design'
import { useNow, useProxy } from '../../api/query'
import { AuthEditor } from './AuthEditor'
import { PortEditor } from './PortEditor'
import { RotatePanel } from './RotatePanel'
import { SelftestPanel } from './SelftestPanel'
import { proxyUri } from './export'
import { AUTH_MODE_LABEL, PROXY_STATE_TONE, formatBytes, formatClock, formatExpiry, usesIPList } from './format'

export interface ProxyDrawerProps {
  proxyId: string | null
  autoRotate?: boolean
  onClose: () => void
}

export function ProxyDrawer({ proxyId, autoRotate = false, onClose }: ProxyDrawerProps) {
  const detail = useProxy(proxyId)
  const now = useNow(30_000)
  const proxy = detail.data?.proxy

  return (
    <Drawer
      open={proxyId != null}
      onClose={onClose}
      title={proxy ? proxy.id : (proxyId ?? '')}
      headerExtra={
        proxy ? (
          <>
            <Badge tone={PROXY_STATE_TONE[proxy.state]}>{proxy.state}</Badge>
            <span className="mono muted">slot {proxy.slot}</span>
          </>
        ) : null
      }
    >
      {detail.isError ? (
        <Notice tone="danger" title="Could not load this proxy">
          {detail.error.message}
        </Notice>
      ) : null}

      {!proxy ? (
        <span className="muted">Loading…</span>
      ) : (
        <>
          <section className="card">
            <h3 className="card-title">Connection</h3>
            <CopyField label="SOCKS5" value={proxyUri(proxy, 'socks5')} />
            <CopyField label="HTTP" value={proxyUri(proxy, 'http')} />
            <dl className="kv">
              <dt>Auth mode</dt>
              <dd>
                {AUTH_MODE_LABEL[proxy.auth_mode]}
                {usesIPList(proxy) ? ` · ${proxy.auth_ip_count ?? detail.data?.auth_ips?.length ?? 0} networks` : ''}
              </dd>
              <dt>Listeners observed</dt>
              <dd>
                socks {proxy.ports_bound.socks ? 'bound' : 'NOT bound'} · http{' '}
                {proxy.ports_bound.http ? 'bound' : 'NOT bound'}
                {proxy.ports_bound.probe_ok === false ? ' · probe failed' : ''}
              </dd>
              <dt>WAN IP</dt>
              <dd className="mono">{proxy.wan_ip || 'unknown'}</dd>
              <dt>Customer</dt>
              <dd>{proxy.customer_name || proxy.customer_id || 'unassigned'}</dd>
              <dt>Expires</dt>
              <dd>{formatExpiry(proxy.expires_at, now).text}</dd>
              <dt>SIM quota</dt>
              <dd>
                {formatBytes(proxy.data_used_bytes ?? 0)}
                {proxy.data_cap_bytes ? ` of ${formatBytes(proxy.data_cap_bytes)}` : ' (no cap set)'}
              </dd>
            </dl>
            {!proxy.ports_bound.socks || !proxy.ports_bound.http ? (
              <Notice tone="danger" title="A listener is missing">
                3proxy can run with zero listeners. The panel reports what it observed, not what was
                configured.
              </Notice>
            ) : null}
          </section>

          <RotatePanel proxy={proxy} autoStart={autoRotate} />

          {detail.data?.last_rotation ? (
            <section className="card">
              <h3 className="card-title">Last rotation</h3>
              <dl className="kv">
                <dt>Result</dt>
                <dd>
                  <Badge tone={detail.data.last_rotation.result === 'changed' ? 'ok' : 'danger'}>
                    {detail.data.last_rotation.result}
                  </Badge>
                </dd>
                <dt>Address</dt>
                <dd className="mono">
                  {detail.data.last_rotation.old_public_ip ?? '?'} →{' '}
                  {detail.data.last_rotation.new_public_ip ?? '?'}
                </dd>
                <dt>Requested</dt>
                <dd>{formatClock(detail.data.last_rotation.requested_at)}</dd>
              </dl>
            </section>
          ) : null}

          <AuthEditor proxy={proxy} />
          <PortEditor proxy={proxy} />
          <SelftestPanel proxy={proxy} />
        </>
      )}
    </Drawer>
  )
}
