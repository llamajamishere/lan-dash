import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
import { get, onAuthError, post } from '../lib/api'

interface AuthStatus { setup_required: boolean; authenticated: boolean; version: string }

interface AuthCtx {
  status: AuthStatus | null
  refresh: () => Promise<void>
  login: (password: string) => Promise<void>
  setup: (password: string) => Promise<void>
  logout: () => Promise<void>
}

const Ctx = createContext<AuthCtx | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)

  const refresh = useCallback(async () => {
    try {
      setStatus(await get<AuthStatus>('/api/auth/status'))
    } catch {
      setStatus({ setup_required: false, authenticated: false, version: '' })
    }
  }, [])

  useEffect(() => {
    void refresh()
    // Any 401/428 from the API invalidates our view of the session.
    const off = onAuthError((code) =>
      setStatus((s) => (s ? { ...s, authenticated: false, setup_required: code === 428 || s.setup_required } : s)),
    )
    return () => {
      off()
    }
  }, [refresh])

  const login = async (password: string) => {
    await post('/api/auth/login', { password })
    await refresh()
  }
  const setup = async (password: string) => {
    await post('/api/auth/setup', { password })
    await refresh()
  }
  const logout = async () => {
    await post('/api/auth/logout')
    await refresh()
  }

  return <Ctx.Provider value={{ status, refresh, login, setup, logout }}>{children}</Ctx.Provider>
}

export function useAuth() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth outside AuthProvider')
  return c
}
