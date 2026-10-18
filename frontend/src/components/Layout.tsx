import { useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useSSE } from '../hooks/useSSE'
import { useDash } from '../lib/store'

const NAV = [
  { to: '/', label: 'Overview', icon: '◉', end: true },
  { to: '/servers', label: 'Servers', icon: '▤' },
  { to: '/smart', label: 'Smart Devices', icon: '💡' },
  { to: '/alerts', label: 'Alerts', icon: '🔔' },
  { to: '/settings', label: 'Settings', icon: '⚙' },
  { to: '/health', label: 'Health', icon: '♥' },
]

/** App shell: sidebar on desktop, slide-out drawer on mobile. Owns the SSE subscription. */
export default function Layout() {
  const { logout, status } = useAuth()
  const connected = useDash((s) => s.sseConnected)
  const firing = useDash((s) => s.alerts.filter((a) => a.state === 'firing').length)
  const [open, setOpen] = useState(false)
  useSSE(true)

  const nav = (
    <nav className="flex flex-1 flex-col gap-1 p-3">
      {NAV.map((n) => (
        <NavLink
          key={n.to}
          to={n.to}
          end={n.end}
          onClick={() => setOpen(false)}
          className={({ isActive }) =>
            `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition ${
              isActive ? 'bg-sky-600/20 text-sky-300' : 'text-slate-300 hover:bg-slate-800'
            }`
          }
        >
          <span className="w-5 text-center" aria-hidden>
            {n.icon}
          </span>
          <span className="flex-1">{n.label}</span>
          {n.to === '/alerts' && firing > 0 && (
            <span className="rounded-full bg-rose-600 px-1.5 text-xs font-semibold text-white">{firing}</span>
          )}
        </NavLink>
      ))}
    </nav>
  )

  const footer = (
    <div className="border-t border-slate-800 p-3 text-xs text-slate-500">
      <div className="mb-2 flex items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${connected ? 'bg-emerald-500' : 'bg-amber-500'}`} />
        {connected ? 'Live' : 'Reconnecting…'}
        {status?.version && <span className="ml-auto">v{status.version}</span>}
      </div>
      <button className="btn-ghost w-full justify-start" onClick={() => void logout()}>
        ⎋ Log out
      </button>
    </div>
  )

  return (
    <div className="flex min-h-screen">
      {/* Desktop sidebar */}
      <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col border-r border-slate-800 bg-slate-900/60 md:flex">
        <Brand />
        {nav}
        {footer}
      </aside>

      {/* Mobile drawer */}
      {open && (
        <div className="fixed inset-0 z-40 md:hidden" onClick={() => setOpen(false)}>
          <div className="absolute inset-0 bg-black/60" />
          <aside className="absolute left-0 top-0 flex h-full w-64 flex-col bg-slate-900" onClick={(e) => e.stopPropagation()}>
            <Brand />
            {nav}
            {footer}
          </aside>
        </div>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex items-center gap-3 border-b border-slate-800 bg-slate-950/90 px-4 py-3 backdrop-blur md:hidden">
          <button className="btn-ghost px-2 text-lg" onClick={() => setOpen(true)} aria-label="Open menu">
            ☰
          </button>
          <span className="font-semibold">LAN Dash</span>
          <span className={`ml-auto h-2 w-2 rounded-full ${connected ? 'bg-emerald-500' : 'bg-amber-500'}`} />
        </header>
        <main className="mx-auto w-full max-w-7xl flex-1 p-4 md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

function Brand() {
  return (
    <div className="flex items-center gap-2 border-b border-slate-800 px-4 py-4">
      <img src="/favicon.svg" alt="" className="h-7 w-7" />
      <span className="text-lg font-semibold tracking-tight">LAN Dash</span>
    </div>
  )
}
