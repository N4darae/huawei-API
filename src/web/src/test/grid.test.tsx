import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ProxiesPage } from '../routes/proxies/ProxiesPage'
import { renderApp } from './render'
import { db } from './state'

const HEADERS = [
  'Status',
  'Proxy',
  'Customer',
  'Expires',
  'WAN IP',
  'Signal',
  'Data (SIM quota)',
  'Ports (observed)',
  'Actions',
]

async function openGrid() {
  const user = userEvent.setup()
  renderApp(<ProxiesPage />)
  await screen.findByText('203.0.113.10:21001')
  return user
}

describe('proxies grid', () => {
  it('renders the operator column set in order', async () => {
    await openGrid()
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent)
    expect(headers).toEqual(HEADERS)
  })

  it('has no checkbox column and no bulk action dock', async () => {
    await openGrid()
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
  })

  it('shows observed bind state, not desired state', async () => {
    await openGrid()

    expect(screen.getAllByLabelText('SOCKS listener observed bound').length).toBeGreaterThan(0)
    const missing = screen.getAllByLabelText('HTTP listener NOT bound')
    expect(missing.length).toBe(2)

    const bound = screen.getAllByLabelText('SOCKS listener observed bound')[0] as HTMLElement
    expect(bound.className).toContain('dot-filled')
    const unbound = missing[0] as HTMLElement
    expect(unbound.className).toContain('dot-hollow')

    expect(screen.getByText('2 proxies have a listener that is not bound')).toBeTruthy()
  })

  it('meters the SIM quota and flags anything at or above 90 percent', async () => {
    await openGrid()
    const meter = screen.getByLabelText('SIM quota used for px02')
    expect(meter.getAttribute('aria-valuenow')).toBe('97')
    expect(within(meter.parentElement as HTMLElement).getByText(/over quota/)).toBeTruthy()
    expect(screen.getByText('1 SIMs are at or above 90% of their quota')).toBeTruthy()
  })

  it('separates an expired proxy from one that expires soon', async () => {
    await openGrid()
    expect(screen.getByText('in 1d')).toBeTruthy()
    expect(screen.getByText('expired 1d ago')).toBeTruthy()
  })

  it('filters client side without another round trip', async () => {
    const user = await openGrid()
    await user.type(screen.getByLabelText('Search'), 'Acme')
    expect(await screen.findByText('1 of 4 shown')).toBeTruthy()
    expect(screen.queryByText('203.0.113.10:21001')).toBeNull()
  })

  it('shows the live step of a running operation in place of the rotate button', async () => {
    const p = db.proxies.find((x) => x.id === 'px01')
    if (p) p.active_operation_id = 'op-live'
    db.ops['op-live'] = {
      cursor: 0,
      frames: [
        {
          id: 'op-live',
          kind: 'rotate',
          subject_type: 'proxy',
          subject_id: 'px01',
          state: 'running',
          step: 'wait_connect',
          pct: 70,
          started_at: db.now,
          deadline_at: db.now + 90_000,
          finished_at: null,
          trigger: 'admin_ui',
        },
      ],
    }

    await openGrid()
    expect(await screen.findByText(/rotate · wait_connect 70%/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Rotate px01' })).toBeNull()
  })

  it('opens the detail drawer from the grid', async () => {
    const user = await openGrid()
    await user.click(screen.getByRole('button', { name: 'Open px01' }))
    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getByText('SOCKS5')).toBeTruthy()
    expect(
      within(drawer).getByText('socks5://cust_px01:Kq7mZr2xTn9wLb4V@203.0.113.10:21001'),
    ).toBeTruthy()
  })
})
