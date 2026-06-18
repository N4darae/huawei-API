import { useEffect } from 'react'
import type { ReactNode } from 'react'
import { Notice } from '../design'
import { setCsrfToken } from '../api/client'
import { useSession } from '../api/query'
import { LoginForm } from './LoginForm'

export function AuthGate({ children }: { children: ReactNode }) {
  const session = useSession()
  const token = session.data?.csrf_token ?? null

  useEffect(() => {
    setCsrfToken(token)
  }, [token])

  if (session.isPending) {
    return (
      <div className="login">
        <span className="muted">Checking session…</span>
      </div>
    )
  }

  if (session.isError) {
    return (
      <div className="login">
        <Notice tone="danger" title="Cannot reach the panel">
          {session.error.message}
        </Notice>
      </div>
    )
  }

  if (!session.data) return <LoginForm />

  return <>{children}</>
}
