import { useState, type FormEvent, type ReactNode } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router-dom'
import { ErrorText } from '../components/ui'
import { useAuth } from '../hooks/useAuth'

export function AuthCard({ title, subtitle, children }: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <div className="card w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-2 text-center">
          <img src="/favicon.svg" alt="" className="h-10 w-10" />
          <h1 className="text-xl font-semibold">{title}</h1>
          {subtitle && <p className="text-sm text-slate-400">{subtitle}</p>}
        </div>
        {children}
      </div>
    </div>
  )
}

export default function Login() {
  const { status, login } = useAuth()
  const nav = useNavigate()
  const loc = useLocation()
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (status?.setup_required) return <Navigate to="/setup" replace />
  if (status?.authenticated) return <Navigate to={(loc.state as any)?.from ?? '/'} replace />

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(password)
      nav((loc.state as any)?.from ?? '/', { replace: true })
    } catch (err: any) {
      setError(err.message ?? 'Login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthCard title="LAN Dash" subtitle="Enter the dashboard password">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <label className="label" htmlFor="pw">
            Password
          </label>
          <input id="pw" type="password" className="input" autoFocus autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        <ErrorText error={error} />
        <button className="btn-primary w-full" disabled={busy || !password}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </AuthCard>
  )
}
