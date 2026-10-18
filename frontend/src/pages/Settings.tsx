import { useEffect, useState, type FormEvent } from 'react'
import { Empty, ErrorText, Modal, PageHeader, StatusDot, Tabs } from '../components/ui'
import { del, get, post, put } from '../lib/api'
import { fmtTime } from '../lib/format'
import { useDash } from '../lib/store'
import { DEVICE_TYPES, SERVER_TYPES, type Device, type DeviceType } from '../lib/types'
import { Toggle } from './Alerts'

type Tab = 'devices' | 'general' | 'security' | 'backup'

export default function Settings() {
  const [tab, setTab] = useState<Tab>('devices')
  return (
    <div>
      <PageHeader title="Settings" />
      <Tabs<Tab>
        value={tab}
        onChange={setTab}
        tabs={[
          { id: 'devices', label: 'Devices' },
          { id: 'general', label: 'General' },
          { id: 'security', label: 'Password' },
          { id: 'backup', label: 'Backup' },
        ]}
      />
      {tab === 'devices' && <Devices />}
      {tab === 'general' && <General />}
      {tab === 'security' && <PasswordChange />}
      {tab === 'backup' && <Backup />}
    </div>
  )
}

// ---------- devices ----------

type DeviceDraft = Omit<Device, 'id' | 'created_at' | 'updated_at'> & { id?: number }
const blankDevice: DeviceDraft = {
  name: '',
  host: '',
  port: 0,
  type: 'ubuntu',
  enabled: true,
  parent_id: null,
  group: null,
  notes: '',
  poll_interval_s: 15,
}

const TYPE_NOTES: Partial<Record<DeviceType, string>> = {
  windows: 'Windows collector arrives in phase 4 (status shows "not yet supported" unless mock mode is on).',
  ping: 'Ping/TCP/HTTP checks arrive in phase 4.',
  hue: 'Philips Hue arrives in phase 5.',
  plug: 'Smart plugs: stub only — not configured.',
  blink: 'Blink (experimental) arrives in phase 7.',
}

function Devices() {
  const devices = useDash((s) => s.devices)
  const states = useDash((s) => s.states)
  const loadDevices = useDash((s) => s.loadDevices)
  const [editing, setEditing] = useState<DeviceDraft | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const remove = async (d: Device) => {
    if (!confirm(`Delete “${d.name}”? Its credentials, metrics and alert state are removed too.`)) return
    setErr(null)
    try {
      await del(`/api/devices/${d.id}`)
      await loadDevices()
    } catch (e: any) {
      setErr(e.message)
    }
  }

  return (
    <>
      <div className="mb-3 flex justify-end">
        <button className="btn-primary" onClick={() => setEditing({ ...blankDevice })}>+ Add device</button>
      </div>
      <ErrorText error={err} />
      {devices.length === 0 ? (
        <Empty>No devices yet. Add your first Ubuntu server to start monitoring.</Empty>
      ) : (
        <div className="card overflow-x-auto p-0">
          <table className="w-full min-w-[720px]">
            <thead className="border-b border-slate-800">
              <tr>
                {['', 'Name', 'Host', 'Type', 'Group', 'Parent', 'Interval', ''].map((h, i) => (
                  <th key={i} className="th">{h}</th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/70">
              {devices.map((d) => (
                <tr key={d.id} className={d.enabled ? '' : 'opacity-50'}>
                  <td className="td"><StatusDot status={d.enabled ? states[d.id]?.status : 'unknown'} /></td>
                  <td className="td font-medium">{d.name}</td>
                  <td className="td font-mono text-xs">{d.host}{d.port ? `:${d.port}` : ''}</td>
                  <td className="td">{d.type}</td>
                  <td className="td text-slate-400">{d.group ?? '—'}</td>
                  <td className="td text-slate-400">{devices.find((p) => p.id === d.parent_id)?.name ?? '—'}</td>
                  <td className="td tabular-nums">{d.poll_interval_s}s</td>
                  <td className="td">
                    <div className="flex justify-end gap-1">
                      <button className="btn-secondary px-2 py-1 text-xs" onClick={() => setEditing({ ...d })}>Edit</button>
                      <button className="btn-ghost px-2 py-1 text-xs text-rose-300" onClick={() => remove(d)}>Delete</button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && (
        <DeviceEditor
          draft={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void loadDevices()
          }}
        />
      )}
    </>
  )
}

interface CredInfo { configured: boolean; kind?: string; username?: string; has_password?: boolean; has_private_key?: boolean; updated_at?: number }

function DeviceEditor({ draft, onClose, onSaved }: { draft: DeviceDraft; onClose: () => void; onSaved: () => void }) {
  const devices = useDash((s) => s.devices)
  const [d, setD] = useState<DeviceDraft>(draft)
  const [cred, setCred] = useState<CredInfo | null>(null)
  // Secret inputs are write-only: never pre-filled, blank = keep existing.
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof DeviceDraft>(k: K, v: DeviceDraft[K]) => setD((x) => ({ ...x, [k]: v }))
  const needsCreds = SERVER_TYPES.includes(d.type)

  useEffect(() => {
    if (!draft.id) return
    get<CredInfo>(`/api/devices/${draft.id}/credentials`)
      .then((c) => {
        setCred(c)
        setUsername(c.username ?? '')
      })
      .catch(() => {})
  }, [draft.id])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const body = {
      name: d.name,
      host: d.host,
      port: Number(d.port) || 0,
      type: d.type,
      enabled: d.enabled,
      parent_id: d.parent_id,
      group: d.group,
      notes: d.notes,
      poll_interval_s: Number(d.poll_interval_s) || 0,
    }
    try {
      const saved = d.id ? await put<Device>(`/api/devices/${d.id}`, body) : await post<Device>('/api/devices', body)
      const credChanged = username !== (cred?.username ?? '') || password || privateKey || passphrase
      if (needsCreds && credChanged) {
        await put(`/api/devices/${saved.id}/credentials`, { kind: 'ssh', username, password, private_key: privateKey, passphrase })
      }
      onSaved()
    } catch (e: any) {
      setErr(e.message)
    } finally {
      setBusy(false)
    }
  }

  const clearCreds = async () => {
    if (!d.id || !confirm('Remove stored credentials for this device?')) return
    try {
      await del(`/api/devices/${d.id}/credentials`)
      setCred({ configured: false })
      setUsername('')
    } catch (e: any) {
      setErr(e.message)
    }
  }

  return (
    <Modal title={d.id ? `Edit ${draft.name}` : 'Add device'} onClose={onClose} wide>
      <form onSubmit={submit} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <label className="label">Name</label>
          <input className="input" value={d.name} onChange={(e) => set('name', e.target.value)} required maxLength={100} />
        </div>
        <div>
          <label className="label">Type</label>
          <select
            className="input"
            value={d.type}
            onChange={(e) => {
              const t = e.target.value as DeviceType
              setD((x) => ({ ...x, type: t, poll_interval_s: x.poll_interval_s === 15 || x.poll_interval_s === 30 ? (t === 'windows' ? 30 : 15) : x.poll_interval_s }))
            }}
          >
            {DEVICE_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
          {TYPE_NOTES[d.type] && <p className="mt-1 text-xs text-amber-300/80">{TYPE_NOTES[d.type]}</p>}
        </div>
        <div>
          <label className="label">Host (IP or hostname)</label>
          <input className="input font-mono" value={d.host} onChange={(e) => set('host', e.target.value)} required placeholder="192.168.1.10" />
        </div>
        <div className="grid grid-cols-2 gap-2">
          <div>
            <label className="label">Port</label>
            <input className="input" type="number" min={0} max={65535} value={d.port || ''} placeholder="default" onChange={(e) => set('port', Number(e.target.value) || 0)} />
          </div>
          <div>
            <label className="label">Poll every (s)</label>
            <input className="input" type="number" min={5} max={3600} value={d.poll_interval_s} onChange={(e) => set('poll_interval_s', Number(e.target.value))} />
          </div>
        </div>
        <div>
          <label className="label">Group (optional)</label>
          <input className="input" value={d.group ?? ''} onChange={(e) => set('group', e.target.value || null)} placeholder="e.g. rack, office" />
        </div>
        <div>
          <label className="label">Parent device (switch/router)</label>
          <select className="input" value={d.parent_id ?? ''} onChange={(e) => set('parent_id', e.target.value ? Number(e.target.value) : null)}>
            <option value="">None</option>
            {devices
              .filter((p) => p.id !== d.id)
              .map((p) => (
                <option key={p.id} value={p.id}>{p.name}</option>
              ))}
          </select>
        </div>
        <div className="sm:col-span-2">
          <label className="label">Notes</label>
          <textarea className="input" rows={2} value={d.notes} onChange={(e) => set('notes', e.target.value)} />
        </div>
        <label className="flex items-center gap-2 text-sm sm:col-span-2">
          <Toggle checked={d.enabled} onChange={(v) => set('enabled', v)} /> Monitoring enabled
        </label>

        {needsCreds && (
          <fieldset className="rounded-lg border border-slate-800 p-3 sm:col-span-2">
            <legend className="px-1 text-xs font-semibold uppercase tracking-wide text-slate-400">SSH credentials (write-only)</legend>
            <p className="mb-3 text-xs text-slate-500">
              {cred?.configured
                ? `Stored: user “${cred.username}”${cred.has_private_key ? ', private key' : ''}${cred.has_password ? ', password' : ''} · updated ${fmtTime(cred.updated_at)}. Leave secret fields blank to keep them.`
                : 'Secrets are encrypted with the server master key and never sent back to the browser. Use a dedicated read-only, non-root user.'}
            </p>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div>
                <label className="label">Username</label>
                <input className="input" value={username} onChange={(e) => setUsername(e.target.value)} autoComplete="off" placeholder="landash-ro" />
              </div>
              <div>
                <label className="label">Password</label>
                <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" placeholder={cred?.has_password ? '•••••• (unchanged)' : ''} />
              </div>
              <div className="sm:col-span-2">
                <label className="label">Private key (OpenSSH PEM, preferred)</label>
                <textarea
                  className="input font-mono text-xs"
                  rows={4}
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  placeholder={cred?.has_private_key ? '(unchanged)' : '-----BEGIN OPENSSH PRIVATE KEY-----'}
                  spellCheck={false}
                />
              </div>
              <div>
                <label className="label">Key passphrase</label>
                <input className="input" type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} autoComplete="new-password" />
              </div>
              {cred?.configured && (
                <div className="flex items-end">
                  <button type="button" className="btn-ghost text-rose-300" onClick={clearCreds}>Remove stored credentials</button>
                </div>
              )}
            </div>
          </fieldset>
        )}

        <div className="sm:col-span-2"><ErrorText error={err} /></div>
        <div className="flex justify-end gap-2 sm:col-span-2">
          <button type="button" className="btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn-primary" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
        </div>
      </form>
    </Modal>
  )
}

// ---------- general ----------

function General() {
  const [mock, setMock] = useState<boolean | null>(null)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    get<{ mock_enabled: boolean }>('/api/settings').then((s) => setMock(s.mock_enabled)).catch((e) => setErr(e.message))
  }, [])
  const toggle = async (v: boolean) => {
    setErr(null)
    try {
      const s = await put<{ mock_enabled: boolean }>('/api/settings', { mock_enabled: v })
      setMock(s.mock_enabled)
      await useDash.getState().loadAll()
    } catch (e: any) {
      setErr(e.message)
    }
  }
  return (
    <div className="card max-w-2xl space-y-4">
      <div className="flex items-start gap-4">
        <div className="flex-1">
          <div className="font-medium">Mock collectors</div>
          <p className="text-sm text-slate-400">
            Replace every real collector with generated data so you can try the dashboard without hardware. Hostnames starting with <code>down</code>,{' '}
            <code>flaky</code>, <code>hot</code> or <code>full</code> simulate failures.
          </p>
        </div>
        {mock != null && <Toggle checked={mock} onChange={toggle} label="Mock collectors" />}
      </div>
      <p className="text-sm text-slate-500">Poll intervals are set per device (Devices tab). Retention: raw samples 1 h in memory, 1-minute averages 24 h in SQLite.</p>
      <ErrorText error={err} />
    </div>
  )
}

// ---------- password ----------

function PasswordChange() {
  const [cur, setCur] = useState('')
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [ok, setOk] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setErr(null)
    setOk(false)
    if (pw.length < 8) return setErr('New password must be at least 8 characters')
    if (pw !== pw2) return setErr('Passwords do not match')
    try {
      await post('/api/auth/password', { current: cur, new: pw })
      setOk(true)
      setCur('')
      setPw('')
      setPw2('')
    } catch (e: any) {
      setErr(e.message)
    }
  }
  return (
    <form onSubmit={submit} className="card max-w-md space-y-4">
      <div>
        <label className="label">Current password</label>
        <input className="input" type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} required />
      </div>
      <div>
        <label className="label">New password</label>
        <input className="input" type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} required />
      </div>
      <div>
        <label className="label">Confirm new password</label>
        <input className="input" type="password" autoComplete="new-password" value={pw2} onChange={(e) => setPw2(e.target.value)} required />
      </div>
      <ErrorText error={err} />
      {ok && <div className="text-sm text-emerald-400">Password changed. Other sessions were signed out.</div>}
      <button className="btn-primary">Change password</button>
    </form>
  )
}

// ---------- backup ----------

function Backup() {
  return (
    <div className="card max-w-2xl space-y-3 text-sm text-slate-300">
      <div className="font-medium">Backups</div>
      <p>
        The server automatically snapshots the database to <code>/var/lib/lan-dash/backups/</code> on every start (before migrations) and keeps the newest 10.
        The master key at <code>/etc/lan-dash/master.key</code> is never included — back it up separately and keep it safe.
      </p>
      <p className="text-slate-400">In-browser export/import (with opt-in, passphrase-encrypted credentials) arrives in phase 6. See the README for manual backup and restore steps.</p>
    </div>
  )
}
