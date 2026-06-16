import { useState } from 'react'
import { Badge, Notice, Sparkline, Tabs } from '../../design'
import { isTerminalOp, useDongle, useNow, useOp, useSlots } from '../../api/query'
import { SIM_PIN_COPY } from '../proxies/OperationProgress'
import { formatAgo, formatBytes } from '../proxies/format'
import { LanIpPanel } from './LanIpPanel'
import { NetModePanel } from './NetModePanel'
import { RebootPanel } from './RebootPanel'
import { SmsPanel } from './SmsPanel'
import { connStatusLabel, connTone, simLocked, simStateLabel, simTone } from './sim'

export function DongleDetail({ dongleId }: { dongleId: string }) {
  const [tab, setTab] = useState('sms')
  const [lanOpId, setLanOpId] = useState<string | null>(null)
  const { op: lanOp, stalled: lanStalled } = useOp(lanOpId)
  const lanRunning = lanOpId != null && (!lanOp || !isTerminalOp(lanOp))

  const detail = useDongle(dongleId, lanRunning)
  const slots = useSlots()
  const now = useNow(5000)

  const dongle = detail.data?.dongle
  const slot = slots.data?.items.find((s) => s.dongle_id === dongleId)
  const signal = detail.data?.signal
  const traffic = detail.data?.traffic

  if (!dongle) {
    return (
      <div className="col">
        {detail.isError && !lanRunning ? (
          <Notice tone="danger" title="Could not load this dongle">
            {detail.error.message}
          </Notice>
        ) : null}
        {lanRunning ? (
          <Notice tone="info" title="Dongle is moving to a new address">
            The old address has gone quiet. This is expected while the change runs.
          </Notice>
        ) : null}
        {!detail.isError ? <span className="muted">Loading…</span> : null}
      </div>
    )
  }

  return (
    <div className="col" style={{ gap: 16 }}>
      {detail.isError && lanRunning ? (
        <Notice tone="info" title="Old address is not answering — expected">
          The dongle stops responding while its LAN address changes. The panel keeps the last known
          state on screen and re-discovers the stick; it will report a failure only if the deadline
          passes.
        </Notice>
      ) : null}

      {detail.isError && !lanRunning ? (
        <Notice tone="danger" title="Dongle is not answering">
          {detail.error.message}
        </Notice>
      ) : null}

      {simLocked(dongle.sim_state) ? (
        <Notice tone="danger" title={SIM_PIN_COPY}>
          The modem reports {simStateLabel(dongle.sim_state)}. The panel cannot unlock it; nothing on
          this dongle works until the SIM is unlocked on a phone and the stick is plugged back in.
        </Notice>
      ) : null}

      <section className="card">
        <div className="row">
          <h3 className="card-title grow">{dongle.id}</h3>
          <Badge tone={connTone(dongle.conn_status)}>{connStatusLabel(dongle.conn_status)}</Badge>
          <Badge tone={simTone(dongle.sim_state)}>SIM {simStateLabel(dongle.sim_state)}</Badge>
          {dongle.reachable === false ? <Badge tone="danger">unreachable</Badge> : null}
        </div>
        <dl className="kv">
          <dt>Slot</dt>
          <dd className="mono">
            {dongle.slot} {slot ? `· ${slot.if_name} · table ${slot.route_table ?? '?'}` : ''}
          </dd>
          <dt>WAN IP</dt>
          <dd className="mono">{dongle.wan_ip || 'none'}</dd>
          <dt>IMEI / ICCID</dt>
          <dd className="mono">
            {dongle.imei} / {dongle.iccid ?? 'unknown'}
          </dd>
          <dt>Carrier</dt>
          <dd>{dongle.carrier ?? 'unknown'}</dd>
          <dt>Firmware</dt>
          <dd className="mono">{dongle.firmware_ver ?? 'unknown'}</dd>
          <dt>Signal</dt>
          <dd>
            {signal ? (
              <span className="row">
                <span className="mono">
                  {signal.bars ?? '?'}/5 · rsrp {signal.rsrp ?? '?'} · sinr {signal.sinr ?? '?'} ·{' '}
                  {signal.band ?? '?'}
                </span>
                <Sparkline
                  label="signal history"
                  values={[signal.rssi ?? 0, signal.rsrp ?? 0, signal.rsrq ?? 0, signal.sinr ?? 0]}
                />
              </span>
            ) : (
              'unknown'
            )}
          </dd>
          <dt>Traffic this month</dt>
          <dd>
            {traffic
              ? `${formatBytes(traffic.month_download ?? 0)} down · ${formatBytes(traffic.month_upload ?? 0)} up`
              : 'unknown'}
          </dd>
          <dt>Last observed</dt>
          <dd>{formatAgo(dongle.observed_at, now)}</dd>
          <dt>Unread SMS</dt>
          <dd>{detail.data?.unread_sms ?? 0}</dd>
        </dl>
      </section>

      <Tabs
        label="Dongle actions"
        active={tab}
        onChange={setTab}
        tabs={[
          { id: 'sms', label: 'SMS', panel: <SmsPanel dongleId={dongle.id} /> },
          {
            id: 'network',
            label: 'Network mode',
            panel: (
              <div style={{ paddingTop: 12 }}>
                <NetModePanel dongle={dongle} />
              </div>
            ),
          },
          {
            id: 'lanip',
            label: 'LAN address',
            panel: (
              <div style={{ paddingTop: 12 }}>
                <LanIpPanel
                  dongle={dongle}
                  slot={slot}
                  op={lanOp}
                  stalled={lanStalled}
                  running={lanRunning}
                  onStart={setLanOpId}
                />
              </div>
            ),
          },
          {
            id: 'reboot',
            label: 'Reboot',
            panel: (
              <div style={{ paddingTop: 12 }}>
                <RebootPanel dongle={dongle} />
              </div>
            ),
          },
        ]}
      />
    </div>
  )
}
