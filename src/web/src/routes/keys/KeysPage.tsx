import { useState } from 'react'
import { Badge, Button, CopyField, Input, Notice } from '../../design'
import { LINK_BASE } from '../../api/keys'
import type { ApiKey } from '../../api/keys'
import {
  useApiKeys,
  useCreateApiKey,
  useCreateLinkToken,
  useNow,
  useRevokeApiKey,
  useRevokeLinkToken,
} from '../../api/query'
import { formatAgo, formatClock } from '../proxies/format'

export const KNOWN_SCOPES = ['rotate', 'status'] as const

function linkUrl(path: string): string {
  const origin = globalThis.location?.origin ?? ''
  return path.startsWith('http') ? path : origin + path
}

function CreateKeyForm({ onCreated }: { onCreated: (secret: string, key: ApiKey) => void }) {
  const create = useCreateApiKey()
  const [name, setName] = useState('')
  const [customerId, setCustomerId] = useState('')
  const [proxyIds, setProxyIds] = useState('')
  const [scopes, setScopes] = useState<string[]>(['rotate', 'status'])

  return (
    <form
      className="card"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim() === '' || scopes.length === 0) return
        create.mutate(
          {
            name: name.trim(),
            customer_id: customerId.trim() === '' ? undefined : customerId.trim(),
            scopes,
            proxy_ids: proxyIds
              .split(/[,\s]+/)
              .map((s) => s.trim())
              .filter((s) => s !== ''),
          },
          {
            onSuccess: (res) => {
              onCreated(res.secret, res.key)
              setName('')
              setCustomerId('')
              setProxyIds('')
            },
          },
        )
      }}
    >
      <h3 className="card-title">Create an API key</h3>
      <div className="row">
        <Input label="Name" className="grow" value={name} onChange={(e) => setName(e.target.value)} required />
        <Input
          label="Customer id"
          className="grow"
          value={customerId}
          placeholder="optional"
          onChange={(e) => setCustomerId(e.target.value)}
        />
      </div>
      <Input
        label="Proxy ids"
        value={proxyIds}
        placeholder="blank = every proxy the customer owns"
        onChange={(e) => setProxyIds(e.target.value)}
      />
      <div className="row">
        <span className="field-label">Scopes</span>
        {KNOWN_SCOPES.map((s) => (
          <label key={s} className="row">
            <input
              type="checkbox"
              checked={scopes.includes(s)}
              onChange={(e) =>
                setScopes((prev) => (e.target.checked ? [...prev, s] : prev.filter((x) => x !== s)))
              }
            />
            <span className="mono">{s}</span>
          </label>
        ))}
      </div>
      {scopes.length === 0 ? <Notice tone="warn" title="A key with no scope can do nothing" /> : null}
      {create.isError ? (
        <Notice tone="danger" title="Could not create the key">
          {create.error.message}
        </Notice>
      ) : null}
      <div className="row">
        <Button type="submit" variant="primary" busy={create.isPending} disabled={scopes.length === 0}>
          Create key
        </Button>
      </div>
    </form>
  )
}

function KeyRow({ apiKey }: { apiKey: ApiKey }) {
  const revoke = useRevokeApiKey()
  const createLink = useCreateLinkToken()
  const revokeLink = useRevokeLinkToken()
  const [linkOnce, setLinkOnce] = useState<string | null>(null)
  const now = useNow(30_000)
  const revoked = apiKey.revoked_at != null

  return (
    <section className="card">
      <div className="row">
        <span className="identity" style={{ gap: 0 }}>
          <span style={{ fontWeight: 600 }}>{apiKey.name}</span>
          <span className="faint mono">
            {apiKey.prefix}… · created {formatClock(apiKey.created_at)} · last used{' '}
            {formatAgo(apiKey.last_used_at, now)}
          </span>
        </span>
        {revoked ? <Badge tone="danger">revoked</Badge> : <Badge tone="ok">active</Badge>}
        {apiKey.scopes.map((s) => (
          <Badge key={s} tone="info">
            {s}
          </Badge>
        ))}
        <Button
          variant="danger"
          disabled={revoked}
          busy={revoke.isPending}
          onClick={() => revoke.mutate(apiKey.id)}
          aria-label={`Revoke key ${apiKey.name}`}
        >
          Revoke key
        </Button>
      </div>

      {apiKey.proxy_ids && apiKey.proxy_ids.length > 0 ? (
        <span className="faint mono">scoped to {apiKey.proxy_ids.join(', ')}</span>
      ) : null}

      <div className="col">
        <span className="field-label">Customer rotate links</span>
        <ul className="list">
          {(apiKey.link_tokens ?? []).map((t) => (
            <li key={t.id} className="list-item" style={{ cursor: 'default' }}>
              <div className="row">
                <span className="mono">
                  {LINK_BASE}/{t.id.slice(0, 6)}…
                </span>
                {t.revoked_at != null ? (
                  <Badge tone="danger">revoked</Badge>
                ) : (
                  <Badge tone="ok">live</Badge>
                )}
                <span className="faint">created {formatClock(t.created_at)}</span>
                <Button
                  variant="danger"
                  disabled={t.revoked_at != null}
                  busy={revokeLink.isPending && revokeLink.variables === t.id}
                  onClick={() => revokeLink.mutate(t.id)}
                  aria-label={`Revoke link token ${t.id}`}
                >
                  Revoke link
                </Button>
              </div>
            </li>
          ))}
          {(apiKey.link_tokens ?? []).length === 0 ? (
            <li className="list-empty">No rotate link issued for this key.</li>
          ) : null}
        </ul>

        {linkOnce ? (
          <>
            <Notice tone="warn" title="This link is shown once">
              It is not stored in a readable form. Send it to the customer now; if you lose it, revoke
              the link and issue another one. Revoking a link does not revoke the API key.
            </Notice>
            <CopyField label="Customer rotate link" value={linkUrl(linkOnce)} />
            <div className="row">
              <Button onClick={() => setLinkOnce(null)}>I sent it — hide the link</Button>
            </div>
          </>
        ) : null}

        {createLink.isError ? (
          <Notice tone="danger" title="Could not create the link">
            {createLink.error.message}
          </Notice>
        ) : null}

        <div className="row">
          <Button
            busy={createLink.isPending}
            disabled={revoked}
            onClick={() =>
              createLink.mutate(apiKey.id, { onSuccess: (res) => setLinkOnce(res.url) })
            }
          >
            Create rotate link
          </Button>
          <span className="muted">
            Created and revoked independently of the key itself.
          </span>
        </div>
      </div>
    </section>
  )
}

export function KeysPage() {
  const list = useApiKeys()
  const [secret, setSecret] = useState<{ value: string; name: string } | null>(null)

  return (
    <div className="page">
      <div className="page-col">
      <div className="page-head">
        <h1 className="page-title">API keys</h1>
        <span className="muted">{list.data?.items.length ?? 0} keys</span>
      </div>

      {secret ? (
        <section className="card">
          <Notice tone="warn" title={`Secret for “${secret.name}” — shown exactly once`}>
            The panel stores only a hash. Once this panel is dismissed the secret cannot be recovered
            and the key has to be replaced.
          </Notice>
          <CopyField label="API key secret" value={secret.value} />
          <div className="row">
            <Button variant="primary" onClick={() => setSecret(null)}>
              I saved it — hide the secret
            </Button>
          </div>
        </section>
      ) : null}

      <CreateKeyForm onCreated={(value, key) => setSecret({ value, name: key.name })} />

      {list.isError ? (
        <Notice tone="danger" title="Could not load keys">
          {list.error.message}
        </Notice>
      ) : null}

      {(list.data?.items ?? []).map((k) => (
        <KeyRow key={k.id} apiKey={k} />
      ))}

      {list.data && list.data.items.length === 0 ? (
        <div className="list-empty">No API keys yet.</div>
      ) : null}
      </div>
    </div>
  )
}
