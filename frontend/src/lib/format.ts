// Formatting helpers. All timestamps from the API are UTC unix seconds and
// rendered in the browser's local time zone.

export function fmtBytes(n?: number | null, digits = 1): string {
  if (n == null || isNaN(n)) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${units[i]}`
}

export function fmtBps(n?: number | null): string {
  if (n == null) return '—'
  const bits = n * 8
  const units = ['bps', 'Kbps', 'Mbps', 'Gbps']
  let i = 0
  let v = bits
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function fmtPct(n?: number | null): string {
  return n == null ? '—' : `${n.toFixed(1)}%`
}

export function fmtTime(ts?: number | null): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString()
}

export function fmtClock(ts?: number | null): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleTimeString()
}

export function fmtDuration(secs?: number | null): string {
  if (secs == null || secs < 0) return '—'
  secs = Math.floor(secs)
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  const m = Math.floor((secs % 3600) / 60)
  const s = secs % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

export function fmtAgo(ts?: number | null): string {
  if (!ts) return 'never'
  return `${fmtDuration(Date.now() / 1000 - ts)} ago`
}

export function fmtMetricValue(metric: string, v?: number | null): string {
  if (v == null) return '—'
  if (metric.endsWith('_pct')) return fmtPct(v)
  if (metric === 'temp_c') return `${v.toFixed(1)} °C`
  if (metric.endsWith('_bps')) return fmtBps(v)
  if (metric === 'latency_ms') return `${v.toFixed(1)} ms`
  if (metric === 'device_offline') return v ? 'offline' : 'online'
  if (metric === 'ssh_key_changed') return v ? 'changed' : 'ok'
  return v.toFixed(2)
}

export function pctColor(p?: number | null): string {
  if (p == null) return '#475569'
  if (p >= 90) return '#f43f5e'
  if (p >= 75) return '#f59e0b'
  return '#22c55e'
}
