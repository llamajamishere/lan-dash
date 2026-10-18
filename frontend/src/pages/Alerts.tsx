import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Empty, ErrorText, Modal, PageHeader, Pager, SeverityBadge, Tabs } from '../components/ui'
import { useNow } from '../hooks/useNow'
import { del, get, post, put } from '../lib/api'
import { fmtDuration, fmtMetricValue, fmtTime } from '../lib/format'
import { useDash } from '../lib/store'
import { DEVICE_TYPES, type AlertHistory, type AlertRule, type AlertState, type Page, type Severity } from '../lib/types'

type Tab = 'active' | 'rules' | 'history'

const METRIC_LABELS: Record<string, string> = {
  cpu_pct: 'CPU %',
  mem_pct: 'RAM %',
  swap_pct: 'Swap %',
  disk_pct: 'Disk % (fullest mount)',
  temp_c: 'Temperature °C (hottest sensor)',
  load1: 'Load average (1 min)',
  net_rx_bps: 'Network download (bytes/s)',
  net_tx_bps: 'Network upload (bytes/s)',
  latency_ms: 'Latency ms',
  battery_pct: 'Battery %',
  device_offline: 'Device offline (1 = offline)',
  ssh_key_changed: 'SSH host key changed (1 = changed)',
}
const OPS: Record<string, string> = { gt: '>', lt: '<', eq: '=' }

export default function Alerts() {
  const [tab, setTab] = useState<Tab>('active')
  const alerts = useDash((s) => s.alerts)
  return (
    <div>
      <PageHeader title="Alerts" />
      <Tabs<Tab>
        value={tab}
        onChange={setTab}
        tabs={[
          { id: 'active', label: `Active (${alerts.length})` },
          { id: 'rules', label: 'Rules' },
          { id: 'history', label: 'History' },
        ]}
      />
      {tab === 'active' && <ActiveAlerts />}
      {tab === 'rules' && <Rules />}
      {tab === 'history' && <History />}
    </div>
  )
}

// ---------- active ----------

function ActiveAlerts() {
  const alerts = useDash((s) => s.alerts)
  const loadAlerts = useDash((s) => s.loadAlerts)
  const now = useNow(1000)
  const [err, setErr] = useState<string | null>(null)
  const [muting, setMuting] = useState<AlertState | null>(null)

  const act = async (a: AlertState, action: 'ack' | 'unmute' | 'mute', duration_s?: number) => {
    setErr(null)
    try {
      await post(`/api/alerts/${a.id}/${action}`, duration_s != null ? { duration_s } : {})
      await loadAlerts()
    } catch (e: any) {
      setErr(e.message)
    }
  }

  if (!alerts.length) return <Empty>✅ All clear — no active or pending alerts.</Empty>
  return (
    <>
      <ErrorText error={err} />
      <div className="card overflow-x-auto p-0">
        <table className="w-full min-w-[760px]">
          <thead className="border-b border-slate-800">
            <tr>
              {['Severity', 'Device', 'Rule', 'Value', 'State', 'Since', 'Duration', ''].map((h) => (
                <th key={h} className="th">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/70">
            {alerts.map((a) => {
              const since = a.fired_at ?? a.pending_since
              const muted = a.muted_until != null && a.muted_until > now
              return (
                <tr key={a.id} className={a.acked_at || muted ? 'opacity-60' : ''}>
                  <td className="td"><SeverityBadge severity={a.severity} /></td>
                  <td className="td font-medium">{a.device_name}</td>
                  <td className="td">{a.rule_name}</td>
                  <td className="td tabular-nums">{fmtMetricValue(a.metric, a.value)}</td>
                  <td className="td">
                    <div className="flex flex-wrap gap-1">
                      <span className={`rounded px-1.5 py-0.5 text-xs ${a.state === 'firing' ? 'bg-rose-600/30 text-rose-200' : 'bg-amber-600/20 text-amber-200'}`}>{a.state}</span>
                      {a.suppressed && <span className="rounded bg-slate-700 px-1.5 py-0.5 text-xs" title="Parent device is down">suppressed</span>}
                      {a.acked_at && <span className="rounded bg-sky-800/50 px-1.5 py-0.5 text-xs text-sky-200">acked</span>}
                      {muted && <span className="rounded bg-slate-700 px-1.5 py-0.5 text-xs">muted until {new Date(a.muted_until! * 1000).toLocaleTimeString()}</span>}
                    </div>
                  </td>
                  <td className="td text-xs text-slate-400">{fmtTime(since)}</td>
                  <td className="td tabular-nums">{since ? fmtDuration(now - since) : '—'}</td>
                  <td className="td">
                    <div className="flex justify-end gap-1">
                      {a.state === 'firing' && !a.acked_at && (
                        <button className="btn-secondary px-2 py-1 text-xs" onClick={() => act(a, 'ack')}>Ack</button>
                      )}
                      {muted ? (
                        <button className="btn-secondary px-2 py-1 text-xs" onClick={() => act(a, 'unmute')}>Unmute</button>
                      ) : (
                        <button className="btn-secondary px-2 py-1 text-xs" onClick={() => setMuting(a)}>Mute</button>
                      )}
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {muting && (
        <Modal title={`Mute “${muting.rule_name}” on ${muting.device_name}`} onClose={() => setMuting(null)}>
          <p className="mb-4 text-sm text-slate-400">Muted alerts stay visible but will not send notifications.</p>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {[
              ['1 hour', 3600],
              ['4 hours', 14400],
              ['24 hours', 86400],
              ['7 days', 604800],
            ].map(([label, secs]) => (
              <button
                key={label}
                className="btn-secondary"
                onClick={() => {
                  void act(muting, 'mute', secs as number)
                  setMuting(null)
                }}
              >
                {label}
              </button>
            ))}
          </div>
        </Modal>
      )}
    </>
  )
}

// ---------- rules ----------

type RuleDraft = Omit<AlertRule, 'id' | 'created_at' | 'updated_at'> & { id?: number }

const blankRule: RuleDraft = {
  name: '',
  device_id: null,
  group: null,
  device_type: null,
  metric: 'cpu_pct',
  operator: 'gt',
  threshold: 90,
  duration_s: 60,
  resolve_threshold: null,
  severity: 'warning',
  enabled: true,
  cooldown_s: 300,
}

function Rules() {
  const devices = useDash((s) => s.devices)
  const [rules, setRules] = useState<AlertRule[]>([])
  const [metrics, setMetrics] = useState<string[]>(Object.keys(METRIC_LABELS))
  const [editing, setEditing] = useState<RuleDraft | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setRules(await get<AlertRule[]>('/api/alert-rules'))
    } catch (e: any) {
      setErr(e.message)
    }
  }, [])
  useEffect(() => {
    void load()
    get<string[]>('/api/alert-metrics').then(setMetrics).catch(() => {})
  }, [load])

  const toggle = async (r: AlertRule) => {
    setErr(null)
    try {
      const { id, created_at, updated_at, ...body } = r
      void created_at
      void updated_at
      await put(`/api/alert-rules/${id}`, { ...body, enabled: !r.enabled })
      await load()
    } catch (e: any) {
      setErr(e.message)
    }
  }
  const remove = async (r: AlertRule) => {
    if (!confirm(`Delete rule “${r.name}”? Its active alerts will be cleared.`)) return
    try {
      await del(`/api/alert-rules/${r.id}`)
      await load()
      await useDash.getState().loadAlerts()
    } catch (e: any) {
      setErr(e.message)
    }
  }

  const target = (r: AlertRule) => {
    const parts: string[] = []
    if (r.device_id != null) parts.push(devices.find((d) => d.id === r.device_id)?.name ?? `device #${r.device_id}`)
    if (r.group) parts.push(`group “${r.group}”`)
    if (r.device_type) parts.push(`type ${r.device_type}`)
    return parts.length ? parts.join(' · ') : 'all devices'
  }

  return (
    <>
      <div className="mb-3 flex justify-end">
        <button className="btn-primary" onClick={() => setEditing({ ...blankRule })}>+ Add rule</button>
      </div>
      <ErrorText error={err} />
      {rules.length === 0 ? (
        <Empty>No alert rules defined.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="w-full min-w-[760px]">
            <thead className="border-b border-slate-800">
              <tr>
                {['On', 'Name', 'Condition', 'For', 'Target', 'Severity', ''].map((h) => (
                  <th key={h} className="th">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/70">
              {rules.map((r) => (
                <tr key={r.id} className={r.enabled ? '' : 'opacity-50'}>
                  <td className="td">
                    <Toggle checked={r.enabled} onChange={() => toggle(r)} />
                  </td>
                  <td className="td font-medium">{r.name}</td>
                  <td className="td font-mono text-xs">
                    {r.metric} {OPS[r.operator]} {r.threshold}
                    {r.resolve_threshold != null && <span className="text-slate-500"> (resolve {r.operator === 'lt' ? '>' : '<'} {r.resolve_threshold})</span>}
                  </td>
                  <td className="td">{r.duration_s ? fmtDuration(r.duration_s) : 'immediately'}</td>
                  <td className="td text-slate-400">{target(r)}</td>
                  <td className="td"><SeverityBadge severity={r.severity} /></td>
                  <td className="td">
                    <div className="flex justify-end gap-1">
                      <button className="btn-secondary px-2 py-1 text-xs" onClick={() => setEditing({ ...r })}>Edit</button>
                      <button className="btn-ghost px-2 py-1 text-xs text-rose-300" onClick={() => remove(r)}>Delete</button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && (
        <RuleEditor
          draft={editing}
          metrics={metrics}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
        />
      )}
    </>
  )
}

export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition ${checked ? 'bg-sky-600' : 'bg-slate-700'}`}
    >
      <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition ${checked ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  )
}

function RuleEditor({ draft, metrics, onClose, onSaved }: { draft: RuleDraft; metrics: string[]; onClose: () => void; onSaved: () => void }) {
  const devices = useDash((s) => s.devices)
  const [r, setR] = useState<RuleDraft>(draft)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof RuleDraft>(k: K, v: RuleDraft[K]) => setR((x) => ({ ...x, [k]: v }))
  const groups = [...new Set(devices.map((d) => d.group).filter(Boolean))] as string[]

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const { id, ...body } = r as RuleDraft & { created_at?: number; updated_at?: number }
    delete (body as any).created_at
    delete (body as any).updated_at
    try {
      if (id) await put(`/api/alert-rules/${id}`, body)
      else await post('/api/alert-rules', body)
      onSaved()
    } catch (e: any) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const num = (v: string) => (v === '' ? 0 : Number(v))

  return (
    <Modal title={r.id ? 'Edit rule' : 'New rule'} onClose={onClose} wide>
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <label className="label">Name</label>
          <input className="input" value={r.name} onChange={(e) => set('name', e.target.value)} required maxLength={120} />
        </div>
        <div>
          <label className="label">Metric</label>
          <select className="input" value={r.metric} onChange={(e) => set('metric', e.target.value)}>
            {metrics.map((m) => (
              <option key={m} value={m}>{METRIC_LABELS[m] ?? m}</option>
            ))}
          </select>
        </div>
        <div className="grid grid-cols-[90px_1fr] gap-2">
          <div>
            <label className="label">Operator</label>
            <select className="input" value={r.operator} onChange={(e) => set('operator', e.target.value as RuleDraft['operator'])}>
              <option value="gt">&gt;</option>
              <option value="lt">&lt;</option>
              <option value="eq">=</option>
            </select>
          </div>
          <div>
            <label className="label">Threshold</label>
            <input className="input" type="number" step="any" value={r.threshold} onChange={(e) => set('threshold', num(e.target.value))} />
          </div>
        </div>
        <div>
          <label className="label">Resolve threshold (hysteresis, optional)</label>
          <input
            className="input"
            type="number"
            step="any"
            placeholder="same as threshold"
            value={r.resolve_threshold ?? ''}
            onChange={(e) => set('resolve_threshold', e.target.value === '' ? null : Number(e.target.value))}
          />
        </div>
        <div>
          <label className="label">Severity</label>
          <select className="input" value={r.severity} onChange={(e) => set('severity', e.target.value as Severity)}>
            <option value="info">info</option>
            <option value="warning">warning</option>
            <option value="critical">critical</option>
          </select>
        </div>
        <div>
          <label className="label">Duration before firing (seconds)</label>
          <input className="input" type="number" min={0} max={86400} value={r.duration_s} onChange={(e) => set('duration_s', num(e.target.value))} />
        </div>
        <div>
          <label className="label">Cooldown after resolve (seconds)</label>
          <input className="input" type="number" min={0} max={86400} value={r.cooldown_s} onChange={(e) => set('cooldown_s', num(e.target.value))} />
        </div>
        <div className="sm:col-span-2">
          <div className="label">Target (all filters combine; leave blank for all devices)</div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
            <select className="input" value={r.device_id ?? ''} onChange={(e) => set('device_id', e.target.value ? Number(e.target.value) : null)}>
              <option value="">Any device</option>
              {devices.map((d) => (
                <option key={d.id} value={d.id}>{d.name}</option>
              ))}
            </select>
            <input className="input" list="rule-groups" placeholder="Any group" value={r.group ?? ''} onChange={(e) => set('group', e.target.value || null)} />
            <datalist id="rule-groups">{groups.map((g) => <option key={g} value={g} />)}</datalist>
            <select className="input" value={r.device_type ?? ''} onChange={(e) => set('device_type', (e.target.value || null) as RuleDraft['device_type'])}>
              <option value="">Any type</option>
              {DEVICE_TYPES.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </div>
        </div>
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <Toggle checked={r.enabled} onChange={(v) => set('enabled', v)} /> Enabled
        </label>
        <div className="sm:col-span-2"><ErrorText error={err} /></div>
        <div className="flex justify-end gap-2 sm:col-span-2">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save rule'}</button>
        </div>
      </form>
    </Modal>
  )
}

// ---------- history ----------

function History() {
  const [page, setPage] = useState(1)
  const [data, setData] = useState<Page<AlertHistory> | null>(null)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    get<Page<AlertHistory>>(`/api/alerts/history?page=${page}&page_size=25`).then(setData).catch((e) => setErr(e.message))
  }, [page])

  if (err) return <ErrorText error={err} />
  if (!data) return null
  if (!data.items.length) return <Empty>No resolved alerts yet.</Empty>
  return (
    <>
      <div className="card overflow-x-auto p-0">
        <table className="w-full min-w-[720px]">
          <thead className="border-b border-slate-800">
            <tr>
              {['Severity', 'Device', 'Rule', 'Peak', 'Fired', 'Resolved', 'Lasted'].map((h) => (
                <th key={h} className="th">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/70">
            {data.items.map((h) => (
              <tr key={h.id}>
                <td className="td"><SeverityBadge severity={h.severity} /></td>
                <td className="td font-medium">{h.device_name}</td>
                <td className="td">
                  {h.rule_name}
                  {h.acked && <span className="ml-2 rounded bg-sky-800/50 px-1.5 py-0.5 text-xs text-sky-200">acked</span>}
                </td>
                <td className="td tabular-nums">{fmtMetricValue(h.metric, h.peak_value)}</td>
                <td className="td text-xs text-slate-400">{fmtTime(h.fired_at)}</td>
                <td className="td text-xs text-slate-400">{fmtTime(h.resolved_at)}</td>
                <td className="td tabular-nums">{fmtDuration(h.resolved_at - h.fired_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Pager page={data.page} pageSize={data.page_size} total={data.total} onPage={setPage} />
    </>
  )
}
