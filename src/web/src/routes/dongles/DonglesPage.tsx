import { Badge, Notice } from '../../design'
import { useDongles } from '../../api/query'
import { useSearchParam } from '../../router'
import { DongleDetail } from './DongleDetail'
import { connStatusLabel, connTone, simLocked, simStateLabel } from './sim'

export function DonglesPage() {
  const [selected, setSelected] = useSearchParam('dongle')
  const list = useDongles()
  const items = list.data?.items ?? []
  const current = selected ?? items[0]?.id ?? null

  return (
    <div className="page">
      <div className="page-head">
        <h1 className="page-title">Dongles</h1>
        <span className="muted">{items.length} sticks</span>
      </div>

      {list.isError ? (
        <Notice tone="danger" title="Could not load dongles">
          {list.error.message}
        </Notice>
      ) : null}

      <div className="split">
        <ul className="list">
          {items.map((d) => (
            <li key={d.id}>
              <button
                className="list-item"
                aria-current={current === d.id}
                onClick={() => setSelected(d.id)}
              >
                <span className="row">
                  <span className="mono grow">slot {d.slot}</span>
                  <Badge tone={connTone(d.conn_status)}>{connStatusLabel(d.conn_status)}</Badge>
                </span>
                <span className="faint mono">{d.id}</span>
                {simLocked(d.sim_state) ? (
                  <Badge tone="danger">SIM {simStateLabel(d.sim_state)}</Badge>
                ) : null}
              </button>
            </li>
          ))}
          {items.length === 0 && !list.isPending ? <li className="muted">No dongles enrolled.</li> : null}
        </ul>

        <div>{current ? <DongleDetail dongleId={current} /> : <span className="muted">Pick a dongle.</span>}</div>
      </div>
    </div>
  )
}
