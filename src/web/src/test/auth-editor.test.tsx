import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ProxyDrawer } from '../routes/proxies/ProxyDrawer'
import { deriveAuthMode, isPlausibleCidr } from '../routes/proxies/AuthEditor'
import { renderApp } from './render'
import { db } from './state'

async function openDrawer(proxyId: string) {
  const user = userEvent.setup()
  renderApp(<ProxyDrawer proxyId={proxyId} onClose={() => {}} />)
  await screen.findByRole('tab', { name: 'Username / password' })
  return user
}

describe('auth mode derivation', () => {
  it('maps the two switches onto the three contract modes', () => {
    expect(deriveAuthMode(true, false)).toBe('userpass')
    expect(deriveAuthMode(false, true)).toBe('iplist')
    expect(deriveAuthMode(true, true)).toBe('both')
    expect(deriveAuthMode(false, false)).toBeNull()
  })

  it('rejects nonsense CIDRs before they reach the backend', () => {
    expect(isPlausibleCidr('203.0.113.5/32')).toBe(true)
    expect(isPlausibleCidr('198.51.100.0/24')).toBe(true)
    expect(isPlausibleCidr('2001:db8::/32')).toBe(true)
    expect(isPlausibleCidr('999.1.1.1')).toBe(false)
    expect(isPlausibleCidr('not an address')).toBe(false)
    expect(isPlausibleCidr('')).toBe(false)
  })
})

describe('auth editor — both modes are editable', () => {
  it('offers a username/password tab and an IP whitelist tab', async () => {
    await openDrawer('px01')
    expect(screen.getByRole('tab', { name: 'Username / password' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'IP whitelist' })).toBeTruthy()
  })

  it('saves a username and password change through the frozen contract shape', async () => {
    const user = await openDrawer('px01')

    const username = screen.getByLabelText('Username') as HTMLInputElement
    await user.clear(username)
    await user.type(username, 'cust_renamed')
    await user.click(screen.getByLabelText('Generate a new password on save'))
    await user.click(screen.getByRole('button', { name: 'Save credentials' }))

    await screen.findByText('Auth change accepted')
    const sent = db.requests.find((r) => r.startsWith('setAuth px01'))
    expect(sent).toBeTruthy()
    const body = JSON.parse((sent as string).slice('setAuth px01 '.length)) as Record<string, unknown>
    expect(body['auth_mode']).toBe('userpass')
    expect(body['username']).toBe('cust_renamed')
    expect(body['rotate_password']).toBe(true)
  })

  it('lists, adds and removes whitelisted networks', async () => {
    const user = await openDrawer('px03')
    await user.click(screen.getByRole('tab', { name: 'IP whitelist' }))

    await screen.findByText('203.0.113.5/32')
    expect(screen.getByText('198.51.100.0/24')).toBeTruthy()

    await user.type(screen.getByLabelText('Source network (CIDR)'), '192.0.2.99/32')
    await user.click(screen.getByRole('button', { name: 'Add network' }))
    await screen.findByText('192.0.2.99/32')

    await user.click(screen.getByRole('button', { name: 'Remove 203.0.113.5/32' }))
    await waitFor(() => expect(screen.queryByText('203.0.113.5/32')).toBeNull())
  })

  it('refuses to save an IP-list proxy with an empty whitelist', async () => {
    db.authIps['px03'] = []
    const user = await openDrawer('px03')
    await user.click(screen.getByRole('tab', { name: 'IP whitelist' }))

    await screen.findByText('Empty whitelist denies everybody')
    const save = screen.getByRole('button', { name: 'Save auth mode' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
  })

  it('blocks turning both auth methods off', async () => {
    const user = await openDrawer('px01')
    await user.click(screen.getByLabelText('Require a username and password'))

    await screen.findByText('At least one auth method must stay on')
    const save = screen.getByRole('button', { name: 'Save credentials' }) as HTMLButtonElement
    expect(save.disabled).toBe(true)
  })
})
