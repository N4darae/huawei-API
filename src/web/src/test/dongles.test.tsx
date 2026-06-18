import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DongleDetail } from '../routes/dongles/DongleDetail'
import { FRAGMENT_MARKER } from '../routes/dongles/SmsPanel'
import { isIPv4 } from '../routes/dongles/LanIpPanel'
import { renderApp } from './render'
import { baseOp, db, installTape } from './state'

async function openDongle(id: string) {
  const user = userEvent.setup()
  renderApp(<DongleDetail dongleId={id} />)
  await screen.findByRole('tab', { name: 'SMS' })
  return user
}

describe('SMS inbox', () => {
  it('marks every fragment with a visible [fragment] marker', async () => {
    await openDongle('dg-1')

    await screen.findByText(/part one of a very long carrier notice/)
    const markers = screen.getAllByText(FRAGMENT_MARKER)
    expect(markers.length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('1 of these are message fragments')).toBeTruthy()
  })

  it('sends a message through the contract shape', async () => {
    const user = await openDongle('dg-1')

    await user.type(screen.getByLabelText('To'), '+84900000009')
    await user.type(screen.getByLabelText('Message'), 'test from the panel')
    await user.click(screen.getByRole('button', { name: 'Send SMS' }))

    await screen.findByText('Queued on the modem')
    const sent = db.requests.find((r) => r.startsWith('sendSms dg-1'))
    expect(sent).toBeTruthy()
    const body = JSON.parse((sent as string).slice('sendSms dg-1 '.length)) as Record<string, unknown>
    expect(body['to']).toEqual(['+84900000009'])
    expect(body['body']).toBe('test from the panel')
  })
})

describe('SIM PIN lock', () => {
  it('gets its own copy instead of a generic device error', async () => {
    await openDongle('dg-2')
    expect(
      await screen.findByText('SIM is PIN-locked — unlock it in a phone and re-plug'),
    ).toBeTruthy()
    expect(screen.getByText(/The modem reports PIN required/)).toBeTruthy()
  })
})

describe('LTE-only lock', () => {
  it('warns that the connection will drop before it runs', async () => {
    const user = await openDongle('dg-1')
    await user.click(screen.getByRole('tab', { name: 'Network mode' }))

    await user.click(screen.getByRole('button', { name: 'Apply network mode' }))

    expect(await screen.findByText(/This will drop the connection — confirm/)).toBeTruthy()
    expect(screen.getByText(/Every\s+session through this dongle drops/)).toBeTruthy()

    const confirm = screen.getByRole('button', { name: /Yes, drop the connection and switch to lte/ })
    installTape('op-netmode-dg-1', [
      baseOp({
        id: 'op-netmode-dg-1',
        kind: 'set_net_mode',
        subject_id: 'dg-1',
        step: 'apply',
        state: 'succeeded',
        finished_at: db.now + 1000,
      }),
    ])
    await user.click(confirm)
    await screen.findByText('Radio locked to lte')
  })
})

describe('LAN IP change', () => {
  it('accepts only IPv4 gateways', () => {
    expect(isIPv4('192.168.101.1')).toBe(true)
    expect(isIPv4('192.168.101')).toBe(false)
    expect(isIPv4('192.168.101.999')).toBe(false)
  })

  it('renders re_discovering as progress and never as an error while the old address is quiet', async () => {
    installTape('op-lanip-dg-1', [
      baseOp({ id: 'op-lanip-dg-1', kind: 'set_lan_ip', subject_id: 'dg-1', step: 'apply', pct: 20 }),
      baseOp({
        id: 'op-lanip-dg-1',
        kind: 'set_lan_ip',
        subject_id: 'dg-1',
        step: 're_discovering',
        pct: 60,
      }),
    ])

    const user = await openDongle('dg-1')
    await user.click(screen.getByRole('tab', { name: 'LAN address' }))

    const input = screen.getByLabelText('New gateway address') as HTMLInputElement
    await user.clear(input)
    await user.type(input, '192.168.121.1')
    await user.click(screen.getByRole('button', { name: 'Change LAN address' }))
    await user.click(screen.getByRole('button', { name: /Move the dongle to 192\.168\.121\.1/ }))

    db.dongleGetStatus['dg-1'] = 502

    await waitFor(() => {
      const el = document.querySelector('[data-step="re_discovering"]')
      expect(el).not.toBeNull()
      expect(el?.getAttribute('data-phase')).toBe('current')
    })

    await screen.findByText('Old address is not answering — expected')
    expect(screen.queryByText('Dongle is not answering')).toBeNull()
    expect(screen.getByText('Re-discovering the dongle')).toBeTruthy()
    expect(screen.getByText('old address is expected to be silent')).toBeTruthy()
  })

  it('offers a copy-paste command when the firmware cannot change its LAN address', async () => {
    const d = db.dongles.find((x) => x.id === 'dg-1')
    if (d) d.lan_ip_change_supported = false

    const user = await openDongle('dg-1')
    await user.click(screen.getByRole('tab', { name: 'LAN address' }))

    await screen.findByText(/This firmware does not accept a LAN address change over HiLink/)
    expect(screen.getByText('usbreset 1-1')).toBeTruthy()
  })
})

describe('reboot', () => {
  it('asks for confirmation with the disconnect warning', async () => {
    const user = await openDongle('dg-1')
    await user.click(screen.getByRole('tab', { name: 'Reboot' }))
    await user.click(screen.getByRole('button', { name: 'Reboot dongle' }))
    expect(await screen.findByText('This will drop the connection')).toBeTruthy()
  })
})
