import { Link } from 'react-router-dom'
import { Empty, PageHeader, StatusDot } from '../components/ui'
import { useNow } from '../hooks/useNow'
import { fmtAgo, fmtBps, fmtPct } from '../lib/format'
import { useDash } from '../lib/store'
import { SERVER_TYPES, type DeviceStatus } from '../lib/types'

export function AlertBanner() {
  const alerts = useDash((s) => s.alerts)
  const firing = alerts
    .filter((a) => a.state === 'firing' && a.severity !== 'info')
    .sort((a, b) => Number(b.severity === 'critical') - Number(a.severity === 'critical'))
  const crit = firing.filter((a) => a.severity === 'critical').length
  const warn = firing.filter((a) => a.severity === 'warning').length
  if (!crit && !warn) return null
  return (
    <Link
      to="/alerts"
      className={`mb-5 flex flex-wrap items-center gap-3 rounded-xl border px-4 py-3 text-sm transition hover:brightness-110 ${
        crit ? 'border-rose-700 bg-rose-950/60 text-rose-200' : 'border-amber-700 bg-amber-950/50 text-amber-200'
      }`}
    >
      <span className="text-lg">{crit ? '⛔' : '⚠️'}</span>
      <span className="font-medium">
        {crit > 0 && `${crit} critical`}
        {crit > 0 && warn > 0 && ' · '}
        {warn > 0 && `${warn} warning`} alert{crit + warn === 1 ? '' : 's'} firing
      </span>
      <span className="truncate text-xs opacity-80">
        {firing
          .slice(0, 3)
          .map((a) => `${a.device_name}: ${a.rule_name}`)
          .join(' · ')}
      </span>
      <span className="ml-auto text-xs underline">View alerts →</span>
    </Link>
  )
}

function Tile({ label, value, tone, sub }: { label: string; value: number | string; tone: string; sub?: string }) {
  return (
    <div className="card">
      <div className="text-xs uppercase tracking-wide text-slate-400">{label}</div>
      <div className={`mt-1 text-3xl font-semibold tabular-nums ${tone}`}>{value}</div>
      {sub && <div className="mt-1 text-xs text-slate-500">{sub}</div>}
    </div>
  )
}

export default function Overview() {
  const devices = useDash((s) => s.devices)
  const states = useDash((s) => s.states)
  const alerts = useDash((s) => s.alerts)
  const loaded = useDash((s) => s.loaded)
  useNow(5000) // refresh "ago" labels

  const enabled = devices.filter((d) => d.enabled)
  const statusOf = (id: number): DeviceStatus => states[id]?.status ?? 'unknown'
  const count = (st: DeviceStatus) => enabled.filter((d) => statusOf(d.id) === st).length
  const firing = alerts.filter((a) => a.state === 'firing').length
  const pending = alerts.filter((a) => a.state === 'pending').length

  // Fleet KPIs from latest snapshots of online servers.
  const servers = enabled.filter((d) => SERVER_TYPES.includes(d.type) && statusOf(d.id) === 'online')
  const cpus = servers.map((d) => states[d.id]?.latest?.cpu_pct).filter((v): v is number => v != null)
  const mems = servers.map((d) => states[d.id]?.latest?.mem_pct).filter((v): v is number => v != null)
  let rx = 0
  let tx = 0
  servers.forEach((d) => states[d.id]?.latest?.net?.forEach((n) => ((rx += n.rx_bps), (tx += n.tx_bps))))
  const avg = (a: number[]) => (a.length ? a.reduce((x, y) => x + y, 0) / a.length : null)
  const firingByDevice = new Set(alerts.filter((a) => a.state === 'firing').map((a) => a.device_id))

  return (
    <div>
      <PageHeader title="Overview" />
      <AlertBanner />

      <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Tile label="Online" value={count('online')} tone="text-emerald-400" sub={`of ${enabled.length} monitored`} />
        <Tile label="Offline" value={count('offline')} tone={count('offline') ? 'text-rose-400' : 'text-slate-300'} />
        <Tile label="Unknown" value={count('unknown')} tone="text-slate-300" sub="no data yet / not supported" />
        <Tile label="Alerts" value={firing} tone={firing ? 'text-amber-400' : 'text-slate-300'} sub={pending ? `${pending} pending` : 'none pending'} />
      </div>

      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="card">
          <div className="text-xs uppercase tracking-wide text-slate-400">Avg server CPU</div>
          <div className="mt-1 text-2xl font-semibold tabular-nums">{fmtPct(avg(cpus))}</div>
        </div>
        <div className="card">
          <div className="text-xs uppercase tracking-wide text-slate-400">Avg server RAM</div>
          <div className="mt-1 text-2xl font-semibold tabular-nums">{fmtPct(avg(mems))}</div>
        </div>
        <div className="card">
          <div className="text-xs uppercase tracking-wide text-slate-400">Total server network</div>
          <div className="mt-1 text-lg font-semibold tabular-nums">
            ↓ {fmtBps(rx)} <span className="text-slate-500">·</span> ↑ {fmtBps(tx)}
          </div>
        </div>
      </div>

      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wide text-slate-400">Devices</h2>
      {loaded && devices.length === 0 ? (
        <Empty>
          No devices yet. <Link to="/settings" className="text-sky-400 underline">Add one in Settings</Link>.
        </Empty>
      ) : (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {devices.map((d) => {
            const st = states[d.id]
            const isServer = SERVER_TYPES.includes(d.type)
            const body = (
              <div
                className={`flex items-center gap-3 rounded-lg border px-3 py-2.5 transition ${
                  firingByDevice.has(d.id) ? 'border-rose-800 bg-rose-950/30' : 'border-slate-800 bg-slate-900/60'
                } ${isServer ? 'hover:border-slate-600' : ''} ${d.enabled ? '' : 'opacity-50'}`}
              >
                <StatusDot status={d.enabled ? st?.status : 'unknown'} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm font-medium">{d.name}</div>
                  <div className="truncate text-xs text-slate-500">
                    {d.type} · {d.host}
                  </div>
                </div>
                <div className="text-right text-[11px] text-slate-500">
                  {!d.enabled ? 'disabled' : st?.unsupported ? 'not yet supported' : st?.status === 'online' ? fmtAgo(st.last_seen) : st?.last_seen ? `seen ${fmtAgo(st.last_seen)}` : '—'}
                </div>
              </div>
            )
            return isServer ? (
              <Link key={d.id} to={`/servers/${d.id}`}>
                {body}
              </Link>
            ) : (
              <div key={d.id}>{body}</div>
            )
          })}
        </div>
      )}
    </div>
  )
}
