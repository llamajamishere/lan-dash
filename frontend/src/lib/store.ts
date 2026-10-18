import { create } from 'zustand'
import { get } from './api'
import type { AlertState, Device, DeviceState, SSEEvent, Snapshot } from './types'

export interface HistPoint { ts: number; cpu?: number; mem?: number; rx?: number; tx?: number }

export interface CollectorErrorEvt { ts: number; device_id: number; device_name: string; error: string; kind: string }

const HISTORY_SECS = 300 // sparkline window (5 min)

interface DashState {
  devices: Device[]
  states: Record<number, DeviceState>
  history: Record<number, HistPoint[]>
  alerts: AlertState[]
  collectorErrors: CollectorErrorEvt[]
  sseConnected: boolean
  loaded: boolean

  loadAll: () => Promise<void>
  loadDevices: () => Promise<void>
  loadAlerts: () => Promise<void>
  handleEvent: (e: SSEEvent) => void
  setConnected: (c: boolean) => void
}

function pointFrom(s: Snapshot): HistPoint {
  let rx = 0
  let tx = 0
  s.net?.forEach((n) => {
    rx += n.rx_bps
    tx += n.tx_bps
  })
  return { ts: s.ts, cpu: s.cpu_pct, mem: s.mem_pct, rx, tx }
}

function trim(points: HistPoint[]): HistPoint[] {
  const cutoff = Date.now() / 1000 - HISTORY_SECS
  let i = 0
  while (i < points.length && points[i].ts < cutoff) i++
  return i ? points.slice(i) : points
}

export const useDash = create<DashState>((set, getState) => ({
  devices: [],
  states: {},
  history: {},
  alerts: [],
  collectorErrors: [],
  sseConnected: false,
  loaded: false,

  async loadAll() {
    const [devices, states, alerts] = await Promise.all([
      get<Device[]>('/api/devices'),
      get<DeviceState[]>('/api/status'),
      get<AlertState[]>('/api/alerts'),
    ])
    const sm: Record<number, DeviceState> = {}
    states.forEach((s) => (sm[s.device_id] = s))
    set({ devices, states: sm, alerts, loaded: true })

    // Bootstrap sparklines from the server's in-memory ring buffer.
    const hist: Record<number, HistPoint[]> = {}
    await Promise.all(
      devices
        .filter((d) => d.enabled)
        .map(async (d) => {
          try {
            const pts = await get<any[]>(`/api/devices/${d.id}/snapshots?seconds=${HISTORY_SECS}&lite=1`)
            hist[d.id] = pts.map((p) => ({ ts: p.ts, cpu: p.cpu_pct, mem: p.mem_pct, rx: p.net_rx_bps, tx: p.net_tx_bps }))
          } catch {
            /* ignore */
          }
        }),
    )
    set((s) => ({ history: { ...hist, ...mergeNewer(hist, s.history) } }))
  },

  async loadDevices() {
    const [devices, states] = await Promise.all([get<Device[]>('/api/devices'), get<DeviceState[]>('/api/status')])
    const sm: Record<number, DeviceState> = {}
    states.forEach((s) => (sm[s.device_id] = s))
    set({ devices, states: sm })
  },

  async loadAlerts() {
    set({ alerts: await get<AlertState[]>('/api/alerts') })
  },

  setConnected(c) {
    set({ sseConnected: c })
  },

  handleEvent(e) {
    const d = e.data
    switch (e.type) {
      case 'metric': {
        const snap: Snapshot = d.snapshot
        set((s) => {
          const prev = s.states[d.device_id]
          const st: DeviceState = {
            ...(prev ?? ({ device_id: d.device_id, interval_s: 15 } as DeviceState)),
            status: 'online',
            last_seen: d.last_seen,
            last_attempt: d.ts,
            last_error: '',
            host_key_mismatch: false,
            latest: snap,
          }
          const h = trim([...(s.history[d.device_id] ?? []), pointFrom(snap)])
          return { states: { ...s.states, [d.device_id]: st }, history: { ...s.history, [d.device_id]: h } }
        })
        break
      }
      case 'device_status': {
        set((s) => {
          const prev = s.states[d.device_id] ?? ({ device_id: d.device_id } as DeviceState)
          return {
            states: {
              ...s.states,
              [d.device_id]: { ...prev, status: d.status, last_seen: d.last_seen, last_error: d.error },
            },
          }
        })
        break
      }
      case 'collector_error': {
        set((s) => {
          const prev = s.states[d.device_id]
          const states = prev
            ? { ...s.states, [d.device_id]: { ...prev, last_error: d.error, host_key_mismatch: d.kind === 'host_key_changed' || prev.host_key_mismatch } }
            : s.states
          return { states, collectorErrors: [{ ts: e.ts, ...d }, ...s.collectorErrors].slice(0, 50) }
        })
        break
      }
      case 'alert_fired':
      case 'alert_updated':
      case 'alert_resolved': {
        const a: AlertState = d
        set((s) => {
          const rest = s.alerts.filter((x) => !(x.rule_id === a.rule_id && x.device_id === a.device_id))
          const active = a.state === 'firing' || a.state === 'pending'
          const alerts = active ? [a, ...rest] : rest
          alerts.sort((x, y) => (y.fired_at ?? y.pending_since ?? 0) - (x.fired_at ?? x.pending_since ?? 0))
          return { alerts }
        })
        break
      }
      case 'devices_changed':
        void getState().loadDevices()
        break
    }
  },
}))

// Keep any SSE points that arrived while the bootstrap fetch was in flight.
function mergeNewer(boot: Record<number, HistPoint[]>, live: Record<number, HistPoint[]>) {
  const out: Record<number, HistPoint[]> = {}
  for (const [k, pts] of Object.entries(live)) {
    const id = Number(k)
    const b = boot[id] ?? []
    const last = b.length ? b[b.length - 1].ts : 0
    out[id] = trim([...b, ...pts.filter((p) => p.ts > last)])
  }
  return out
}
