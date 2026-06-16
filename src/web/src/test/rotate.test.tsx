import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ROTATE_STEPS } from '../api/keys'
import { ProxyDrawer } from '../routes/proxies/ProxyDrawer'
import { renderApp } from './render'
import { baseOp, db, installRotateTape, installTape } from './state'

function stepEl(name: string): HTMLElement {
  const el = document.querySelector(`[data-step="${name}"]`)
  if (!el) throw new Error(`step ${name} is not rendered`)
  return el as HTMLElement
}

async function openAndRotate() {
  const user = userEvent.setup()
  renderApp(<ProxyDrawer proxyId="px01" onClose={() => {}} />)
  const button = await screen.findByRole('button', { name: 'Rotate IP' })
  await user.click(button)
  return user
}

describe('rotate flow', () => {
  it('renders the frozen public step sequence', async () => {
    installRotateTape({ proxyId: 'px01' })
    await openAndRotate()

    await waitFor(() => expect(document.querySelector('[data-step="precheck"]')).not.toBeNull())
    const list = screen.getByRole('list', { name: 'Rotate progress' })
    const rendered = within(list)
      .getAllByRole('listitem')
      .map((li) => li.getAttribute('data-step'))
    expect(rendered).toEqual([...ROTATE_STEPS])
  })

  it('never reports success before the backend confirms', async () => {
    installTape('op-rotate-px01', [
      baseOp({ id: 'op-rotate-px01', kind: 'rotate', subject_id: 'px01', step: 'data_off', pct: 30 }),
    ])
    await openAndRotate()

    await waitFor(() => expect(stepEl('data_off').getAttribute('data-phase')).toBe('current'))

    await new Promise((r) => setTimeout(r, 250))

    expect(screen.queryByText(/public IP changed/i)).toBeNull()
    expect(screen.queryByText(/Rotation failed/i)).toBeNull()
    expect(stepEl('done').getAttribute('data-phase')).toBe('pending')
    expect(screen.getByRole('button', { name: /Rotating/ })).toBeTruthy()
  })

  it('walks the steps and only then reports the changed outcome', async () => {
    installRotateTape({ proxyId: 'px01', oldIp: '100.71.4.5', newIp: '100.71.8.8' })
    await openAndRotate()

    await waitFor(() => expect(stepEl('wait_connect').getAttribute('data-phase')).not.toBe('pending'))
    await screen.findByText(/Rotated — public IP changed/)
    expect(screen.getByText(/100\.71\.4\.5 → 100\.71\.8\.8/)).toBeTruthy()
    expect(stepEl('done').getAttribute('data-phase')).toBe('done')
  })

  it('renders "rotated but the IP did not change" as a failure', async () => {
    installRotateTape({ proxyId: 'px01', outcome: 'unchanged', oldIp: '100.71.4.5' })
    await openAndRotate()

    const notice = await screen.findByText(/Rotation FAILED — the public IP did not change/)
    expect(notice).toBeTruthy()
    expect(screen.queryByText(/Rotated — public IP changed/)).toBeNull()
    expect(screen.getByText(/This is a failure, not a\s+success/)).toBeTruthy()
  })

  it('shows a stalled operation honestly instead of spinning forever', async () => {
    installRotateTape({ proxyId: 'px01', stallAtStep: 'wait_connect' })
    await openAndRotate()

    await screen.findByText('Stalled')
    expect(stepEl('wait_connect').getAttribute('data-phase')).toBe('stalled')
    expect(screen.queryByText(/public IP changed/i)).toBeNull()
  })

  it('attaches to the running operation on 409 instead of raising an error', async () => {
    installTape('op-existing', [
      baseOp({ id: 'op-existing', kind: 'rotate', subject_id: 'px01', step: 'hold', pct: 40 }),
    ])
    db.rotateResponse = { kind: 'conflict', opId: 'op-existing' }

    await openAndRotate()

    await screen.findByText('Already running — attached')
    expect(screen.queryByText(/Could not start the rotation/)).toBeNull()
    await waitFor(() => expect(stepEl('hold').getAttribute('data-phase')).toBe('current'))
    expect(screen.getByText('op-existing')).toBeTruthy()
  })

  it('shows the wait when the per-proxy minimum interval is violated', async () => {
    db.rotateResponse = { kind: 'rate_limited', retryAfter: 45 }

    await openAndRotate()

    await screen.findByText('Too soon — per-proxy minimum interval')
    expect(screen.getByText(/Wait 4[0-9]s before rotating again/)).toBeTruthy()
    expect(screen.queryByText(/Could not start the rotation/)).toBeNull()
    expect((screen.getByRole('button', { name: 'Rotate IP' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('gives a SIM PIN lock its own copy', async () => {
    installRotateTape({ proxyId: 'px01', outcome: 'failed', error: 'sim_pin_required' })
    await openAndRotate()

    await screen.findByText('Rotation failed')
    expect(screen.getByText('SIM is PIN-locked — unlock it in a phone and re-plug')).toBeTruthy()
  })
})
