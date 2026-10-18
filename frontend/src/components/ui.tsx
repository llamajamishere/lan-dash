// Small presentational building blocks shared across pages.
import { useEffect, type ReactNode } from 'react'
import { Area, AreaChart, ResponsiveContainer, YAxis } from 'recharts'
import { pctColor } from '../lib/format'
import type { DeviceStatus, Severity } from '../lib/types'

export function StatusDot({ status }: { status?: DeviceStatus }) {
  const c = status === 'online' ? 'bg-emerald-500' : status === 'offline' ? 'bg-rose-500' : 'bg-slate-500'
  return (
    <span className="relative inline-flex h-2.5 w-2.5">
      {status === 'online' && <span className={`absolute inline-flex h-full w-full animate-ping rounded-full ${c} opacity-40`} />}
      <span className={`relative inline-flex h-2.5 w-2.5 rounded-full ${c}`} />
    </span>
  )
}

export function StatusBadge({ status }: { status?: DeviceStatus }) {
  const cls =
    status === 'online'
      ? 'bg-emerald-500/15 text-emerald-300'
      : status === 'offline'
        ? 'bg-rose-500/15 text-rose-300'
        : 'bg-slate-500/20 text-slate-300'
  return <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${cls}`}>{status ?? 'unknown'}</span>
}

const SEV: Record<Severity, string> = {
  critical: 'bg-rose-500/15 text-rose-300 ring-rose-500/40',
  warning: 'bg-amber-500/15 text-amber-300 ring-amber-500/40',
  info: 'bg-sky-500/15 text-sky-300 ring-sky-500/40',
}
const SEV_ICON: Record<Severity, string> = { critical: '⛔', warning: '⚠️', info: 'ℹ️' }

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ${SEV[severity]}`}>
      <span aria-hidden>{SEV_ICON[severity]}</span>
      {severity}
    </span>
  )
}

/** Horizontal usage bar (0–100%). */
export function Bar({ pct, label, right }: { pct?: number | null; label: string; right?: string }) {
  const v = pct == null ? 0 : Math.max(0, Math.min(100, pct))
  return (
    <div>
      <div className="mb-1 flex justify-between text-xs">
        <span className="text-slate-400">{label}</span>
        <span className="tabular-nums text-slate-300">{right ?? (pct == null ? '—' : `${pct.toFixed(1)}%`)}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-slate-800">
        <div className="h-full rounded-full transition-all duration-500" style={{ width: `${v}%`, background: pctColor(pct) }} />
      </div>
    </div>
  )
}

/** Semi-circular gauge drawn with SVG (cheap, no chart lib needed). */
export function Gauge({ pct, label }: { pct?: number | null; label: string }) {
  const v = pct == null ? 0 : Math.max(0, Math.min(100, pct))
  const r = 34
  const len = Math.PI * r
  return (
    <div className="flex flex-col items-center">
      <svg viewBox="0 0 80 46" className="w-full max-w-[96px]">
        <path d="M6 40 A34 34 0 0 1 74 40" fill="none" stroke="#1e293b" strokeWidth="7" strokeLinecap="round" />
        <path
          d="M6 40 A34 34 0 0 1 74 40"
          fill="none"
          stroke={pctColor(pct)}
          strokeWidth="7"
          strokeLinecap="round"
          strokeDasharray={`${(v / 100) * len} ${len}`}
          style={{ transition: 'stroke-dasharray .5s' }}
        />
        <text x="40" y="38" textAnchor="middle" className="fill-slate-100" fontSize="13" fontWeight="600">
          {pct == null ? '—' : `${Math.round(pct)}%`}
        </text>
      </svg>
      <span className="-mt-0.5 text-[11px] uppercase tracking-wide text-slate-400">{label}</span>
    </div>
  )
}

export function Sparkline<T extends object>({
  data,
  dataKey,
  color = '#38bdf8',
  max,
  height = 36,
}: {
  data: T[]
  dataKey: keyof T & string
  color?: string
  max?: number
  height?: number
}) {
  if (data.length < 2) return <div style={{ height }} className="flex items-center text-[11px] text-slate-600">collecting…</div>
  const id = `g-${dataKey}-${color.replace('#', '')}`
  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 0, left: 0 }}>
          <defs>
            <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.35} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <YAxis hide domain={[0, max ?? 'auto']} />
          <Area type="monotone" dataKey={dataKey} stroke={color} strokeWidth={1.5} fill={`url(#${id})`} isAnimationActive={false} connectNulls />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}

export function Modal({ title, onClose, children, wide }: { title: string; onClose: () => void; children: ReactNode; wide?: boolean }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 sm:items-center" onMouseDown={onClose}>
      <div
        className={`card w-full ${wide ? 'max-w-3xl' : 'max-w-lg'} bg-slate-900`}
        onMouseDown={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button className="btn-ghost px-2" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed border-slate-800 p-8 text-center text-sm text-slate-500">{children}</div>
}

export function ErrorText({ error }: { error?: string | null }) {
  if (!error) return null
  return <div className="rounded-lg border border-rose-800 bg-rose-950/50 px-3 py-2 text-sm text-rose-300">{error}</div>
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { id: T; label: ReactNode }[]; value: T; onChange: (t: T) => void }) {
  return (
    <div className="mb-4 flex gap-1 overflow-x-auto border-b border-slate-800">
      {tabs.map((t) => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          className={`whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium transition ${
            value === t.id ? 'border-sky-500 text-sky-300' : 'border-transparent text-slate-400 hover:text-slate-200'
          }`}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

export function Pager({ page, pageSize, total, onPage }: { page: number; pageSize: number; total: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return (
    <div className="mt-3 flex items-center justify-between text-sm text-slate-400">
      <span>
        {total} total · page {page} of {pages}
      </span>
      <div className="flex gap-2">
        <button className="btn-secondary" disabled={page <= 1} onClick={() => onPage(page - 1)}>
          ← Prev
        </button>
        <button className="btn-secondary" disabled={page >= pages} onClick={() => onPage(page + 1)}>
          Next →
        </button>
      </div>
    </div>
  )
}

export function PageHeader({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 className="text-xl font-semibold text-slate-100">{title}</h1>
      <div className="flex flex-wrap items-center gap-2">{children}</div>
    </div>
  )
}
