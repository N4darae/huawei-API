import { Badge, Button, Notice } from '../../design'
import type { Proxy } from '../../api/keys'
import { useSelftest } from '../../api/query'

export function SelftestPanel({ proxy }: { proxy: Proxy }) {
  const test = useSelftest()
  const r = test.data

  return (
    <section className="card">
      <h3 className="card-title">Self test</h3>
      <div className="row">
        <Button busy={test.isPending} onClick={() => test.mutate(proxy.id)}>
          Run self test
        </Button>
        <span className="muted">
          Dials the proxy from the panel and reports the egress address it actually came out of.
        </span>
      </div>

      {test.isError ? (
        <Notice tone="danger" title="Self test could not run">
          {test.error.message}
        </Notice>
      ) : null}

      {r ? (
        <div className="row">
          <Badge tone={r.socks_ok ? 'ok' : 'danger'}>SOCKS5 {r.socks_ok ? 'ok' : 'failed'}</Badge>
          <Badge tone={r.http_ok ? 'ok' : 'danger'}>HTTP {r.http_ok ? 'ok' : 'failed'}</Badge>
          {r.egress_ip ? <span className="mono">egress {r.egress_ip}</span> : null}
          {r.latency_ms != null ? <span className="muted">{r.latency_ms} ms</span> : null}
        </div>
      ) : null}

      {r?.egress_ip && proxy.wan_ip && r.egress_ip !== proxy.wan_ip ? (
        <Notice tone="danger" title="Egress leak — the proxy did not leave through its dongle">
          The self test came out of {r.egress_ip} but this proxy&apos;s dongle reports {proxy.wan_ip}.
          Traffic is leaving via the host uplink; policy routing is wrong.
        </Notice>
      ) : null}

      {r?.error ? (
        <Notice tone="danger" title="Self test error">
          {r.error}
        </Notice>
      ) : null}
    </section>
  )
}
