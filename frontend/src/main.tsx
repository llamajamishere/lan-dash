import { StrictMode, useEffect, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import Layout from './components/Layout'
import { AuthProvider, useAuth } from './hooks/useAuth'
import { useDash } from './lib/store'
import Alerts from './pages/Alerts'
import Health from './pages/Health'
import Login from './pages/Login'
import Overview from './pages/Overview'
import ServerDetail from './pages/ServerDetail'
import Servers from './pages/Servers'
import Settings from './pages/Settings'
import Setup from './pages/Setup'
import SmartDevices from './pages/SmartDevices'
import './index.css'

/** Gate for authenticated routes: routes to /setup or /login as needed and loads initial state. */
function Protected({ children }: { children: ReactNode }) {
  const { status } = useAuth()
  const loc = useLocation()
  const loadAll = useDash((s) => s.loadAll)
  const ok = !!status?.authenticated

  useEffect(() => {
    if (ok) void loadAll().catch(() => {})
  }, [ok, loadAll])

  if (!status) return <Splash />
  if (status.setup_required) return <Navigate to="/setup" replace />
  if (!status.authenticated) return <Navigate to="/login" replace state={{ from: loc.pathname }} />
  return <>{children}</>
}

function Splash() {
  return <div className="flex min-h-screen items-center justify-center text-slate-500">Loading…</div>
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/setup" element={<Setup />} />
      <Route
        element={
          <Protected>
            <Layout />
          </Protected>
        }
      >
        <Route index element={<Overview />} />
        <Route path="servers" element={<Servers />} />
        <Route path="servers/:id" element={<ServerDetail />} />
        <Route path="smart" element={<SmartDevices />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="settings" element={<Settings />} />
        <Route path="health" element={<Health />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <App />
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
)
