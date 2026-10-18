import { useCallback, useEffect, useState } from 'react'
import { ErrorText, PageHeader, Pager, StatusDot } from '../components/ui'
import { useNow } from '../hooks/useNow'
import { get } from '../lib/api'
import { fmtAgo, fmtBytes, fmtDuration, fmtTime } from '../lib/format'
import { useDash } from '../lib/store'
import type { AuditEntry, DeviceState, Page } from '../lib/types'

interface HealthInfo {
  version: string
  started_at: number
  uptime_secs: number
  db_size_bytes: number
  schema_version: number
  go_version: string
  goroutines: number
  heap_bytes: number
  sse_clients: number
  mock_enabled: boolean
  collectors: DeviceState[]
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="card">
      <div className="text-xs uppercase tracking-wide text-slate-400">{label}</div>
      <div className="mt-1 truncate text-lg font-semibold tabular-nums">{value}</div>
    </div>
  )
}

export default function Health() {
  const devices = useDash((s) => s.devices)
  const collectorErrors = useDash((s) => s.collectorErrors)
  const [h, setH] = useState<HealthInfo | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const now = useNow(10000)

  const load = useCallback(() => {
    get<HealthInfo>('/api/health').then(setH).catch((e) => setErr(e.message))
  }, [])
  useEffect(load, [load, Math.floor(now / 10)])

  const name = (id: number | null) => (id == null ? '—' : devices.find((d) => d.id === id)?.name ?? `#${id}`)

  return (
    <div>
      <PageHeader title="Health" />
      <ErrorText error={err} />
      {h && (
        <div className="mb-6 grid grid-cols-2 gap-3 md:grid-cols-4">
          <Stat label="Uptime" value={fmtDuration(h.uptime_secs)} />
          <Stat label="Database size" value={fmtBytes(h.db_size_bytes)} />
          <Stat label="Version" value={`${h.version} · schema v${h.schema_version}`} />
          <Stat label="Mode" value={h.mock_enabled ? 'Mock collectors' : 'Live collectors'} />
          <Stat label="Heap" value={fmtBytes(h.heap_bytes)} />
          <Stat label="Goroutines" value={h.goroutines} />
          <Stat label="Live clients (SSE)" value={h.sse_clients} />
          <Stat label="Go" value={h.go_version} />
        </div>
      )}

      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-400">Collectors</h2>
      <div className="card mb-6 overflow-x-auto p-0">
        <table className="w-full min-w-[720px]">
          <thead className="border-b border-slate-800">
            <tr>
              {['', 'Device', 'Collector', 'Last success', 'Poll time', 'Failures', 'Last error'].map((x, i) => (
                <th key={i} className="th">{x}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/70">
            {(h?.collectors ?? []).map((c) => (
              <tr key={c.device_id}>
                <td className="td"><StatusDot status={c.status} /></td>
                <td className="td font-medium">{name(c.device_id)}</td>
                <td className="td">{c.collector}{c.unsupported && <span className="ml-1 text-xs text-slate-500">(not yet supported)</span>}</td>
                <td className="td text-xs text-slate-400">{c.last_seen ? `${fmtTime(c.last_seen)} (${fmtAgo(c.last_seen)})` : 'never'}</td>
                <td className="td tabular-nums">{c.poll_duration_ms ? `${c.poll_duration_ms} ms` : '—'}</td>
                <td className={`td tabular-nums ${c.consecutive_failures ? 'text-rose-400' : ''}`}>{c.consecutive_failures}</td>
                <td className="td max-w-xs truncate text-xs text-slate-400" title={c.last_error}>{c.last_error || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {collectorErrors.length > 0 && (
        <>
          <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-400">Recent collector errors (this session)</h2>
          <div className="card mb-6 max-h-64 space-y-1 overflow-y-auto font-mono text-xs">
            {collectorErrors.map((e, i) => (
              <div key={i} className="text-slate-400">
                <span className="text-slate-500">{fmtTime(e.ts)}</span> <span className="text-slate-200">{e.device_name}</span> [{e.kind}] {e.error}
              </div>
            ))}
          </div>
        </>
      )}

      <AuditLog name={name} />
    </div>
  )
}

function AuditLog({ name }: { name: (id: number | null) => string }) {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<Page<AuditEntry> | null>(null)
  useEffect(() => {
    get<Page<AuditEntry>>(`/api/audit?page=${page}&page_size=25`).then(setData).catch(() => {})
  }, [page])
  return (
    <>
      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-slate-400">Audit log</h2>
      <div className="card overflow-x-auto p-0">
        <table className="w-full min-w-[720px]">
          <thead className="border-b border-slate-800">
            <tr>
              {['Time', 'Action', 'Device', 'Source IP', 'Details'].map((x) => (
                <th key={x} className="th">{x}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/70">
            {data?.items.map((a) => (
              <tr key={a.id}>
                <td className="td whitespace-nowrap text-xs text-slate-400">{fmtTime(a.ts)}</td>
                <td className="td font-mono text-xs">{a.action}</td>
                <td className="td">{name(a.device_id)}</td>
                <td className="td font-mono text-xs">{a.source_ip}</td>
                <td className="td max-w-sm truncate font-mono text-xs text-slate-500" title={a.payload}>{a.payload}</td>
              </tr>
            ))}
            {data && data.items.length === 0 && (
              <tr><td className="td text-slate-500" colSpan={5}>No entries yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>
      {data && data.total > data.page_size && <Pager page={data.page} pageSize={data.page_size} total={data.total} onPage={setPage} />}
    </>
  )
}
