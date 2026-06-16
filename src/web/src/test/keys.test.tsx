import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { KeysPage } from '../routes/keys/KeysPage'
import { renderApp } from './render'
import { db } from './state'

const SECRET = 'dgl_zz99_THISISTHEONLYTIMEYOUSEEIT'

async function openKeys() {
  const user = userEvent.setup()
  renderApp(<KeysPage />)
  await screen.findByText('Acme production')
  return user
}

describe('api keys', () => {
  it('shows a new key secret exactly once and never again', async () => {
    const user = await openKeys()

    await user.type(screen.getByLabelText('Name'), 'New customer')
    await user.click(screen.getByRole('button', { name: 'Create key' }))

    await screen.findByText(SECRET)
    expect(screen.getByText(/shown exactly once/)).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'I saved it — hide the secret' }))
    await waitFor(() => expect(screen.queryByText(SECRET)).toBeNull())

    expect(screen.getByText('New customer')).toBeTruthy()
    expect(screen.queryByText(SECRET)).toBeNull()
  })

  it('creates a rotate link and shows it once', async () => {
    const user = await openKeys()

    await user.click(screen.getByRole('button', { name: 'Create rotate link' }))

    await screen.findByText(/\/r\/9f3c1d2e8a7b4c5d6e0f$/)
    expect(screen.getByText('This link is shown once')).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'I sent it — hide the link' }))
    await waitFor(() => expect(screen.queryByText(/9f3c1d2e8a7b4c5d6e0f/)).toBeNull())
  })

  it('revokes a link token without revoking the api key', async () => {
    const user = await openKeys()

    await user.click(screen.getByRole('button', { name: 'Revoke link token lt-existing-1' }))

    await waitFor(() => {
      const token = db.keys[0]?.link_tokens?.[0]
      expect(token?.revoked_at).not.toBeNull()
    })
    expect(db.keys[0]?.revoked_at).toBeNull()

    const card = screen.getByText('Acme production').closest('section') as HTMLElement
    await waitFor(() => expect(within(card).getAllByText('revoked').length).toBe(1))
    expect(within(card).getByText('active')).toBeTruthy()
  })

  it('revokes an api key without touching its links', async () => {
    const user = await openKeys()

    await user.click(screen.getByRole('button', { name: 'Revoke key Acme production' }))

    await waitFor(() => expect(db.keys[0]?.revoked_at).not.toBeNull())
    expect(db.keys[0]?.link_tokens?.[0]?.revoked_at).toBeNull()
  })
})
