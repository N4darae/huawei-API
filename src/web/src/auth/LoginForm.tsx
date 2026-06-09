import { useState } from 'react'
import { Button, Input, Notice } from '../design'
import { useLogin } from '../api/query'

export function LoginForm() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const login = useLogin()
  const err = login.error
  const waitFor = err?.retryAfterS

  return (
    <form
      className="login"
      onSubmit={(e) => {
        e.preventDefault()
        login.mutate({ username, password })
      }}
    >
      <h1 className="page-title">dongled</h1>
      <Input
        label="Username"
        name="username"
        autoComplete="username"
        value={username}
        onChange={(e) => setUsername(e.target.value)}
        required
      />
      <Input
        label="Password"
        name="password"
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
        required
      />
      {err ? (
        <Notice tone="danger" title={err.status === 429 ? 'Too many attempts' : 'Sign in failed'}>
          {waitFor != null ? `Wait ${waitFor}s before trying again.` : err.message}
        </Notice>
      ) : null}
      <Button type="submit" variant="primary" size="md" busy={login.isPending}>
        Sign in
      </Button>
    </form>
  )
}
