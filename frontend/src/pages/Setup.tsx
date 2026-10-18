import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { ErrorText } from '../components/ui'
import { useAuth } from '../hooks/useAuth'
import { AuthCard } from './Login'

/** First-run page: sets the shared dashboard password. */
export default function Setup() {
  const { status, setup } = useAuth()
  const nav = useNavigate()
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (status && !status.setup_required) return <Navigate to={status.authenticated ? '/' : '/login'} replace />

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (pw.length < 8) return setError('Password must be at least 8 characters')
    if (pw !== pw2) return setError('Passwords do not match')
    setBusy(true)
    setError(null)
    try {
      await setup(pw)
      nav('/', { replace: true })
    } catch (err: any) {
      setError(err.message ?? 'Setup failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthCard title="Welcome to LAN Dash" subtitle="Choose a password to protect this dashboard. Anyone on your LAN with this password can view and control devices.">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <label className="label" htmlFor="pw">
            New password
          </label>
          <input id="pw" type="password" className="input" autoFocus autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
        </div>
        <div>
          <label className="label" htmlFor="pw2">
            Confirm password
          </label>
          <input id="pw2" type="password" className="input" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} />
        </div>
        <ErrorText error={error} />
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? 'Saving…' : 'Set password & continue'}
        </button>
      </form>
    </AuthCard>
  )
}
