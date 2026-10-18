import { Link } from 'react-router-dom'
import { Bar, Empty, Gauge, PageHeader, Sparkline, StatusBadge } from '../components/ui'
import { useNow } from '../hooks/useNow'
import { fmtAgo, fmtBps, fmtBytes, fmtDuration } from '../lib/format'
import { useDash, type HistPoint } from '../lib/store'
import { SERVER_TYPES, type Device, type DeviceState } from '../lib/types'

const EMPTY: HistPoint[] = []

export function ServerCard({ device, state }: { device: Device; state?: DeviceState }) {
  const hist = useDash((s) => s.history[device.id] ?? EMPTY)
  const snap = state?.latest
  const online = state?.status === 'online'
  const rootDisk = snap?.disks?.find((d) => d.mount === '/' || d.mount === 'C:') ?? snap?.disks?.[0]
  const maxDisk = snap?.disks?.reduce((m, d) => (d.pct > m.pct ? d : m), snap.disks[0])
  const temp = snap?.temps?.reduce((m, t) => Math.max(m, t.c), -Infinity)
  let rx = 0
  let tx = 0
  snap?.net?.forEach((n) => ((rx += n.rx_bps), (tx += n.tx_bps)))

  return (
    <Link to={`/servers/${device.id}`} className="card block transition hover:border-slate-600">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-semibold">{device.name}</div>
          <div className="truncate text-xs text-slate-500">
            {device.host} · {device.type}
            {state?.collector === 'mock' && <span className="ml-1 rounded bg-violet-500/20 px-1 text-violet-300">mock</span>}
          </div>
        </div>
        <StatusBadge status={device.enabled ? state?.status : undefined} />
      </div>

      {state?.host_key_mismatch && (
        <div className="mb-3 rounded-md bg-rose-950/60 px-2 py-1 text-xs text-rose-300">⚠ SSH host key changed — polling stopped</div>
      )}
      {!online && state?.last_error && !state.host_key_mismatch && (
        <div className="mb-3 truncate rounded-md bg-slate-800/80 px-2 py-1 text-xs text-slate-400" title={state.last_error}>
          {state.last_error}
        </div>
      )}

      <div className={`grid grid-cols-3 gap-2 ${online ? '' : 'opacity-40'}`}>
        <Gauge pct={online ? snap?.cpu_pct : null} label="CPU" />
        <Gauge pct={online ? snap?.mem_pct : null} label="RAM" />
        <Gauge pct={online ? rootDisk?.pct : null} label={rootDisk ? `Disk ${rootDisk.mount}` : 'Disk'} />
      </div>

      <div className="mt-3 grid grid-cols-2 gap-3">
        <div>
          <div className="text-[11px] text-slate-500">CPU 5 min</div>
          <Sparkline data={hist} dataKey="cpu" color="#38bdf8" max={100} />
        </div>
        <div>
          <div className="text-[11px] text-slate-500">RAM 5 min</div>
          <Sparkline data={hist} dataKey="mem" color="#a78bfa" max={100} />
        </div>
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-xs text-slate-400">
        <span>↓ {online ? fmtBps(rx) : '—'}</span>
        <span>↑ {online ? fmtBps(tx) : '—'}</span>
        <span>Load {snap?.load1 != null && online ? snap.load1.toFixed(2) : '—'}</span>
        <span>{temp != null && isFinite(temp) && online ? `${temp.toFixed(0)} °C` : 'Temp —'}</span>
        <span>Up {online ? fmtDuration(snap?.uptime_secs) : '—'}</span>
        <span>{online ? `polled ${fmtAgo(state?.last_seen)}` : `seen ${fmtAgo(state?.last_seen)}`}</span>
      </div>
      {maxDisk && maxDisk !== rootDisk && online && (
        <div className="mt-3">
          <Bar pct={maxDisk.pct} label={`Fullest: ${maxDisk.mount}`} right={`${maxDisk.pct.toFixed(0)}% of ${fmtBytes(maxDisk.total_bytes)}`} />
        </div>
      )}
    </Link>
  )
}

export default function Servers() {
  const devices = useDash((s) => s.devices)
  const states = useDash((s) => s.states)
  const loaded = useDash((s) => s.loaded)
  useNow(5000)
  const servers = devices.filter((d) => SERVER_TYPES.includes(d.type))

  return (
    <div>
      <PageHeader title="Servers">
        <Link to="/settings" className="btn-secondary">
          + Add server
        </Link>
      </PageHeader>
      {loaded && servers.length === 0 ? (
        <Empty>No Ubuntu or Windows servers configured yet.</Empty>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {servers.map((d) => (
            <ServerCard key={d.id} device={d} state={states[d.id]} />
          ))}
        </div>
      )}
    </div>
  )
}
