import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { Bar, Empty, ErrorText, Gauge, StatusBadge, Tabs } from '../components/ui'
import { useNow } from '../hooks/useNow'
import { get, post } from '../lib/api'
import { fmtAgo, fmtBps, fmtBytes, fmtClock, fmtDuration, fmtPct } from '../lib/format'
import { useDash } from '../lib/store'

type Range = '1h' | '24h'
type Tab = 'processes' | 'services' | 'docker' | 'disks' | 'network' | 'temps'

interface ChartPoint { ts: number; cpu?: number | null; mem?: number | null; rx?: number | null; tx?: number | null }

/** Loads chart history: raw ring-buffer samples (1h) or persisted 1-minute averages (24h). */
function useChartData(id: number, range: Range, tick: number) {
  const [data, setData] = useState<ChartPoint[]>([])
  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        if (range === '1h') {
          const pts = await get<any[]>(`/api/devices/${id}/snapshots?seconds=3600&lite=1`)
          if (!cancelled) setData(pts.map((p) => ({ ts: p.ts, cpu: p.cpu_pct, mem: p.mem_pct, rx: p.net_rx_bps, tx: p.net_tx_bps })))
        } else {
          const rows = await get<{ ts: number; metric: string; avg: number }[]>(`/api/devices/${id}/metrics?hours=24`)
          const byTs = new Map<number, ChartPoint>()
          const key: Record<string, keyof ChartPoint> = { cpu_pct: 'cpu', mem_pct: 'mem', net_rx_bps: 'rx', net_tx_bps: 'tx' }
          rows.forEach((r) => {
            const k = key[r.metric]
            if (!k) return
            const p = byTs.get(r.ts) ?? { ts: r.ts }
            ;(p as any)[k] = r.avg
            byTs.set(r.ts, p)
          })
          if (!cancelled) setData([...byTs.values()].sort((a, b) => a.ts - b.ts))
        }
      } catch {
        if (!cancelled) setData([])
      }
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [id, range, tick])
  return data
}

const axisTick = { fill: '#64748b', fontSize: 11 }
const tooltipStyle = { background: '#0f172a', border: '1px solid #334155', borderRadius: 8, fontSize: 12 }

export default function ServerDetail() {
  const id = Number(useParams().id)
  const device = useDash((s) => s.devices.find((d) => d.id === id))
  const state = useDash((s) => s.states[id])
  const loaded = useDash((s) => s.loaded)
  const [range, setRange] = useState<Range>('1h')
  const [tab, setTab] = useState<Tab>('processes')
  const [msg, setMsg] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const now = useNow(30000) // refresh charts every 30s
  const data = useChartData(id, range, Math.floor(now / 30))
  const snap = state?.latest
  const online = state?.status === 'online'

  const totals = useMemo(() => {
    let rx = 0
    let tx = 0
    snap?.net?.forEach((n) => ((rx += n.rx_bps), (tx += n.tx_bps)))
    return { rx, tx }
  }, [snap])

  if (!device) return loaded ? <Empty>Device not found. <Link className="text-sky-400 underline" to="/servers">Back to servers</Link></Empty> : null

  const resetHostKey = async () => {
    if (!confirm('Only reset the stored host key if you know why it changed (e.g. OS reinstall). Continue?')) return
    setErr(null)
    try {
      await post(`/api/devices/${id}/hostkey/reset`)
      setMsg('Host key cleared. The new key will be trusted on the next connection.')
    } catch (e: any) {
      setErr(e.message)
    }
  }

  const rootDisk = snap?.disks?.find((d) => d.mount === '/' || d.mount === 'C:') ?? snap?.disks?.[0]
  const fmtTick = (ts: number) => (range === '1h' ? fmtClock(ts).slice(0, 5) : new Date(ts * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }))

  return (
    <div>
      <Link to="/servers" className="mb-3 inline-block text-sm text-slate-400 hover:text-slate-200">
        ← Servers
      </Link>
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <h1 className="text-xl font-semibold">{device.name}</h1>
        <StatusBadge status={device.enabled ? state?.status : undefined} />
        <span className="text-sm text-slate-500">
          {device.host}
          {device.port ? `:${device.port}` : ''} · {device.type} · polled every {device.poll_interval_s}s
          {state?.collector === 'mock' && ' · mock data'}
        </span>
        <span className="ml-auto text-xs text-slate-500">last poll {fmtAgo(state?.last_seen)} {state?.poll_duration_ms ? `(${state.poll_duration_ms} ms)` : ''}</span>
      </div>

      {state?.host_key_mismatch && (
        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-rose-700 bg-rose-950/50 p-3 text-sm text-rose-200">
          <span>⚠ The SSH host key for this server changed. Polling is paused until you verify the server and reset the stored key.</span>
          <button className="btn-danger ml-auto" onClick={resetHostKey}>
            Reset host key
          </button>
        </div>
      )}
      {msg && <div className="mb-4 rounded-lg border border-emerald-800 bg-emerald-950/40 px-3 py-2 text-sm text-emerald-300">{msg}</div>}
      <div className="mb-4">
        <ErrorText error={err ?? (!online && state?.last_error && !state.host_key_mismatch ? state.last_error : null)} />
      </div>

      <div className="mb-5 grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
        <div className="card flex justify-center"><Gauge pct={online ? snap?.cpu_pct : null} label="CPU" /></div>
        <div className="card flex justify-center"><Gauge pct={online ? snap?.mem_pct : null} label="RAM" /></div>
        <div className="card flex justify-center"><Gauge pct={online ? snap?.swap_pct : null} label="Swap" /></div>
        <div className="card flex justify-center"><Gauge pct={online ? rootDisk?.pct : null} label={`Disk ${rootDisk?.mount ?? ''}`} /></div>
        <div className="card text-sm">
          <div className="text-xs uppercase text-slate-400">Load</div>
          <div className="mt-1 tabular-nums">{snap?.load1 != null ? `${snap.load1.toFixed(2)} / ${snap.load5?.toFixed(2)} / ${snap.load15?.toFixed(2)}` : '—'}</div>
          <div className="mt-2 text-xs uppercase text-slate-400">Uptime</div>
          <div className="tabular-nums">{fmtDuration(snap?.uptime_secs)}</div>
        </div>
        <div className="card text-sm">
          <div className="text-xs uppercase text-slate-400">Memory</div>
          <div className="mt-1 tabular-nums">{fmtBytes(snap?.mem_used)} / {fmtBytes(snap?.mem_total)}</div>
          <div className="mt-2 text-xs uppercase text-slate-400">Network</div>
          <div className="tabular-nums">↓ {fmtBps(totals.rx)} ↑ {fmtBps(totals.tx)}</div>
        </div>
      </div>

      <div className="card mb-5">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-slate-400">History</h2>
          <div className="flex gap-1">
            {(['1h', '24h'] as Range[]).map((r) => (
              <button key={r} className={r === range ? 'btn-primary' : 'btn-secondary'} onClick={() => setRange(r)}>
                {r === '1h' ? '1 h (raw)' : '24 h (1-min avg)'}
              </button>
            ))}
          </div>
        </div>
        {data.length < 2 ? (
          <Empty>Not enough data yet.</Empty>
        ) : (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="h-56">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={data}>
                  <CartesianGrid stroke="#1e293b" vertical={false} />
                  <XAxis dataKey="ts" tickFormatter={fmtTick} tick={axisTick} minTickGap={40} />
                  <YAxis domain={[0, 100]} tick={axisTick} width={32} unit="%" />
                  <Tooltip contentStyle={tooltipStyle} labelFormatter={(ts) => fmtClock(ts as number)} formatter={(v: number) => fmtPct(v)} />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Line dataKey="cpu" name="CPU" stroke="#38bdf8" dot={false} isAnimationActive={false} strokeWidth={1.5} connectNulls />
                  <Line dataKey="mem" name="RAM" stroke="#a78bfa" dot={false} isAnimationActive={false} strokeWidth={1.5} connectNulls />
                </LineChart>
              </ResponsiveContainer>
            </div>
            <div className="h-56">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={data}>
                  <CartesianGrid stroke="#1e293b" vertical={false} />
                  <XAxis dataKey="ts" tickFormatter={fmtTick} tick={axisTick} minTickGap={40} />
                  <YAxis tick={axisTick} width={60} tickFormatter={(v) => fmtBps(v)} />
                  <Tooltip contentStyle={tooltipStyle} labelFormatter={(ts) => fmtClock(ts as number)} formatter={(v: number) => fmtBps(v)} />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Line dataKey="rx" name="Download" stroke="#22c55e" dot={false} isAnimationActive={false} strokeWidth={1.5} connectNulls />
                  <Line dataKey="tx" name="Upload" stroke="#f59e0b" dot={false} isAnimationActive={false} strokeWidth={1.5} connectNulls />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}
      </div>

      <div className="card">
        <Tabs<Tab>
          value={tab}
          onChange={setTab}
          tabs={[
            { id: 'processes', label: `Processes (${snap?.processes?.length ?? 0})` },
            { id: 'services', label: `Services (${snap?.services?.length ?? 0})` },
            { id: 'docker', label: `Docker (${snap?.containers?.length ?? 0})` },
            { id: 'disks', label: `Disks (${snap?.disks?.length ?? 0})` },
            { id: 'network', label: `Network (${snap?.net?.length ?? 0})` },
            { id: 'temps', label: `Temps (${snap?.temps?.length ?? 0})` },
          ]}
        />
        <DetailTab tab={tab} snap={snap} />
      </div>
    </div>
  )
}

function Table({ head, children, empty }: { head: string[]; children: React.ReactNode; empty: boolean }) {
  if (empty) return <Empty>No data reported by this host.</Empty>
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[540px]">
        <thead className="border-b border-slate-800">
          <tr>{head.map((h) => <th key={h} className="th">{h}</th>)}</tr>
        </thead>
        <tbody className="divide-y divide-slate-800/70">{children}</tbody>
      </table>
    </div>
  )
}

function stateColor(s: string) {
  if (/^(running|active|up)/i.test(s)) return 'text-emerald-400'
  if (/^(failed|dead|exited|restarting)/i.test(s)) return 'text-rose-400'
  return 'text-slate-400'
}

function DetailTab({ tab, snap }: { tab: Tab; snap?: import('../lib/types').Snapshot }) {
  switch (tab) {
    case 'processes':
      return (
        <Table head={['PID', 'User', 'CPU', 'Mem', 'RSS', 'Command']} empty={!snap?.processes?.length}>
          {snap?.processes?.map((p) => (
            <tr key={p.pid}>
              <td className="td tabular-nums text-slate-400">{p.pid}</td>
              <td className="td">{p.user}</td>
              <td className="td tabular-nums">{fmtPct(p.cpu_pct)}</td>
              <td className="td tabular-nums">{fmtPct(p.mem_pct)}</td>
              <td className="td tabular-nums">{fmtBytes(p.rss_kb * 1024)}</td>
              <td className="td max-w-xs truncate font-mono text-xs" title={p.command}>{p.command}</td>
            </tr>
          ))}
        </Table>
      )
    case 'services':
      return (
        <Table head={['Service', 'State', 'Description']} empty={!snap?.services?.length}>
          {[...(snap?.services ?? [])]
            .sort((a, b) => Number(a.state !== 'failed') - Number(b.state !== 'failed') || a.name.localeCompare(b.name))
            .map((s) => (
              <tr key={s.name}>
                <td className="td font-mono text-xs">{s.name}</td>
                <td className={`td ${stateColor(s.state)}`}>{s.state}</td>
                <td className="td text-slate-400">{s.description}</td>
              </tr>
            ))}
        </Table>
      )
    case 'docker':
      return (
        <Table head={['Name', 'Image', 'State', 'Status', 'ID']} empty={!snap?.containers?.length}>
          {snap?.containers?.map((c) => (
            <tr key={c.id}>
              <td className="td font-medium">{c.name}</td>
              <td className="td font-mono text-xs text-slate-400">{c.image}</td>
              <td className={`td ${stateColor(c.state)}`}>{c.state}</td>
              <td className="td text-slate-400">{c.status}</td>
              <td className="td font-mono text-xs text-slate-500">{c.id.slice(0, 12)}</td>
            </tr>
          ))}
        </Table>
      )
    case 'disks':
      return snap?.disks?.length ? (
        <div className="grid gap-4 sm:grid-cols-2">
          {snap.disks.map((d) => (
            <div key={d.mount} className="rounded-lg border border-slate-800 p-3">
              <Bar pct={d.pct} label={d.mount} right={`${fmtBytes(d.used_bytes)} / ${fmtBytes(d.total_bytes)}`} />
              <div className="mt-1 text-xs text-slate-500">{d.filesystem}</div>
            </div>
          ))}
        </div>
      ) : (
        <Empty>No data reported by this host.</Empty>
      )
    case 'network':
      return (
        <Table head={['Interface', 'Download', 'Upload', 'RX total', 'TX total']} empty={!snap?.net?.length}>
          {snap?.net?.map((n) => (
            <tr key={n.iface}>
              <td className="td font-mono text-xs">{n.iface}</td>
              <td className="td tabular-nums">{fmtBps(n.rx_bps)}</td>
              <td className="td tabular-nums">{fmtBps(n.tx_bps)}</td>
              <td className="td tabular-nums text-slate-400">{fmtBytes(n.rx_bytes)}</td>
              <td className="td tabular-nums text-slate-400">{fmtBytes(n.tx_bytes)}</td>
            </tr>
          ))}
        </Table>
      )
    case 'temps':
      return (
        <Table head={['Sensor', 'Temperature']} empty={!snap?.temps?.length}>
          {snap?.temps?.map((t) => (
            <tr key={t.label}>
              <td className="td">{t.label}</td>
              <td className={`td tabular-nums ${t.c >= 80 ? 'text-rose-400' : t.c >= 65 ? 'text-amber-400' : ''}`}>{t.c.toFixed(1)} °C</td>
            </tr>
          ))}
        </Table>
      )
  }
}
