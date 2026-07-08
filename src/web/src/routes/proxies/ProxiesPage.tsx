import { useMemo, useState } from 'react'
import { Badge, Button, Dot, Input, Meter, Notice, Select, Table } from '../../design'
import type { Column } from '../../design'
import type { Proxy, ProxyState } from '../../api/keys'
import { useNow, useOp, useProxies } from '../../api/query'
import { useSearchParam } from '../../router'
import { ExportPanel } from './ExportPanel'
import { ProxyDrawer } from './ProxyDrawer'
import {
  PROXY_STATE_TONE,
  formatBytes,
  formatExpiry,
  signalText,
  signalTone,
} from './format'

const STATES: ProxyState[] = ['active', 'suspended', 'disabled', 'expired', 'degraded', 'unknown']

function ProxyCell({ proxy }: { proxy: Proxy }) {
  const s = proxy.ports_bound.socks
  const h = proxy.ports_bound.http
  return (
    <span className="col" style={{ gap: 0 }}>
      <span className="row mono" style={{ gap: 4, flexWrap: 'nowrap' }}>
        <span>
          {proxy.host}:{proxy.socks_port}
        </span>
        <Dot
          filled={s}
          tone={s ? 'ok' : 'danger'}
          label={s ? 'SOCKS listener observed bound' : 'SOCKS listener NOT bound'}
        />
      </span>
      <span className="row faint mono" style={{ gap: 4, flexWrap: 'nowrap' }}>
        <span>http {proxy.http_port}</span>
        <Dot
          filled={h}
          tone={h ? 'ok' : 'danger'}
          label={h ? 'HTTP listener observed bound' : 'HTTP listener NOT bound'}
        />
        <span>· slot {proxy.slot}</span>
      </span>
      <span className="faint mono">
        {proxy.username}
        {proxy.ports_bound.probe_ok === false ? ' · probe failed' : ''}
      </span>
    </span>
  )
}

function RowOp({ opId }: { opId: string }) {
  const { op, stalled } = useOp(opId)
  if (!op) return <span className="faint">starting…</span>
  if (stalled) return <Badge tone="warn">stalled at {op.step}</Badge>
  return (
    <Badge tone="info">
      {op.kind} · {op.step} {op.pct}%
    </Badge>
  )
}

function ActionsCell({
  proxy,
  onOpen,
  onRotate,
}: {
  proxy: Proxy
  onOpen: () => void
  onRotate: () => void
}) {
  if (proxy.active_operation_id) {
    return (
      <span className="row">
        <RowOp opId={proxy.active_operation_id} />
        <Button onClick={onOpen}>Open</Button>
      </span>
    )
  }
  return (
    <span className="row">
      <Button variant="primary" onClick={onRotate} aria-label={`Rotate ${proxy.id}`}>
        Rotate
      </Button>
      <Button onClick={onOpen} aria-label={`Open ${proxy.id}`}>
        Open
      </Button>
    </span>
  )
}

export function ProxiesPage() {
  const [selected, setSelected] = useSearchParam('proxy')
  const [autoRotate, setAutoRotate] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [state, setState] = useState<'' | ProxyState>('')
  const [expiring, setExpiring] = useState('')
  const [term, setTerm] = useState('')
  const now = useNow(30_000)

  const query = useMemo(
    () => ({
      state: state === '' ? undefined : state,
      expiring_within_days: expiring === '' ? undefined : Number(expiring),
    }),
    [state, expiring],
  )

  const list = useProxies(query)
  const items = list.data?.items ?? []

  const rows = useMemo(() => {
    const t = term.trim().toLowerCase()
    if (t === '') return items
    return items.filter((p) =>
      [p.id, p.host, p.username, p.wan_ip ?? '', p.customer_name ?? '', String(p.slot)]
        .join(' ')
        .toLowerCase()
        .includes(t),
    )
  }, [items, term])

  const unbound = items.filter((p) => !p.ports_bound.socks || !p.ports_bound.http).length
  const overQuota = items.filter(
    (p) => (p.data_cap_bytes ?? 0) > 0 && (p.data_used_bytes ?? 0) / (p.data_cap_bytes as number) >= 0.9,
  ).length

  const columns: ReadonlyArray<Column<Proxy>> = [
    {
      key: 'status',
      header: 'Status',
      width: '96px',
      cell: (p) => <Badge tone={PROXY_STATE_TONE[p.state]}>{p.state}</Badge>,
    },
    {
      key: 'proxy',
      header: 'Proxy',
      width: '230px',
      cell: (p) => <ProxyCell proxy={p} />,
    },
    {
      key: 'customer',
      header: 'Customer',
      width: '120px',
      cell: (p) =>
        p.customer_name || p.customer_id ? (
          <span>{p.customer_name || p.customer_id}</span>
        ) : (
          <span className="faint">unassigned</span>
        ),
    },
    {
      key: 'expires',
      header: 'Expires',
      width: '100px',
      cell: (p) => {
        const e = formatExpiry(p.expires_at, now)
        return e.tone === 'neutral' ? <span className="muted">{e.text}</span> : <Badge tone={e.tone}>{e.text}</Badge>
      },
    },
    {
      key: 'wan',
      header: 'WAN IP',
      width: '116px',
      cell: (p) => (p.wan_ip ? <span className="mono">{p.wan_ip}</span> : <span className="faint">no address</span>),
    },
    {
      key: 'signal',
      header: 'Signal & quota',
      width: '190px',
      cell: (p) => (
        <span className="col" style={{ gap: 4 }}>
          <span className={'mono ' + (signalTone(p.signal_bars) === 'danger' ? '' : 'muted')}>
            {signalText(p.signal_bars)}
          </span>
          <Meter
            label={`SIM quota used for ${p.id}`}
            value={p.data_used_bytes ?? 0}
            max={p.data_cap_bytes ?? 0}
            format={formatBytes}
          />
        </span>
      ),
    },
    {
      key: 'actions',
      header: 'Actions',
      width: '176px',
      sticky: 'right',
      cell: (p) => (
        <ActionsCell
          proxy={p}
          onOpen={() => {
            setAutoRotate(false)
            setSelected(p.id)
          }}
          onRotate={() => {
            setAutoRotate(true)
            setSelected(p.id)
          }}
        />
      ),
    },
  ]

  return (
    <div className="page">
      <div className="page-col" style={{ paddingBottom: 0 }}>
      <div className="page-head">
        <h1 className="page-title">Proxies</h1>
        <span className="muted">
          {rows.length} of {items.length} shown
        </span>
        <span className="grow" />
        <Button variant="primary" size="md" onClick={() => setExporting(true)}>
          Export list
        </Button>
      </div>

      <div className="toolbar">
        <Input
          label="Search"
          value={term}
          placeholder="id, host, customer, WAN IP"
          onChange={(e) => setTerm(e.target.value)}
          style={{ width: 260 }}
        />
        <Select
          label="State"
          value={state}
          onChange={(e) => setState(e.target.value as ProxyState | '')}
          style={{ width: 160 }}
        >
          <option value="">any state</option>
          {STATES.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </Select>
        <Select
          label="Expiring within"
          value={expiring}
          onChange={(e) => setExpiring(e.target.value)}
          style={{ width: 160 }}
        >
          <option value="">any time</option>
          <option value="3">3 days</option>
          <option value="7">7 days</option>
          <option value="30">30 days</option>
        </Select>
      </div>

      {unbound > 0 || overQuota > 0 ? (
        <div className="page-alerts">
          {unbound > 0 ? (
            <Notice
              compact
              tone="danger"
              title={`${unbound} proxies have a listener that is not bound`}
              hint={
                '3proxy exits 0 with no listener when its config is rejected, so "running" means ' +
                'nothing. These rows show what the backend actually observed.'
              }
            />
          ) : null}

          {overQuota > 0 ? (
            <Notice compact tone="warn" title={`${overQuota} SIMs are at or above 90% of their quota`} />
          ) : null}
        </div>
      ) : null}

      {list.isError ? (
        <Notice tone="danger" title="Could not load proxies">
          {list.error.message}
        </Notice>
      ) : null}
      </div>

      <div className="page-col-bleed">
        <Table
          caption="Proxies"
          columns={columns}
          rows={rows}
          rowKey={(p) => p.id}
          selectedKey={selected}
          onRowActivate={(p) => {
            setAutoRotate(false)
            setSelected(p.id)
          }}
          empty={list.isPending ? 'Loading proxies…' : 'No proxies match this filter.'}
        />
      </div>

      <ProxyDrawer
        proxyId={selected}
        autoRotate={autoRotate}
        onClose={() => {
          setAutoRotate(false)
          setSelected(null)
        }}
      />

      <ExportPanel open={exporting} proxies={rows} onClose={() => setExporting(false)} />
    </div>
  )
}
