// Shared API types (mirror backend/internal/db + collector structs).

export type DeviceType = 'ubuntu' | 'windows' | 'hue' | 'blink' | 'plug' | 'ping'
export const DEVICE_TYPES: DeviceType[] = ['ubuntu', 'windows', 'hue', 'blink', 'plug', 'ping']
export const SERVER_TYPES: DeviceType[] = ['ubuntu', 'windows']

export interface Device {
  id: number
  name: string
  host: string
  port: number
  type: DeviceType
  enabled: boolean
  parent_id: number | null
  group: string | null
  notes: string
  poll_interval_s: number
  created_at: number
  updated_at: number
}

export interface DiskStat { mount: string; filesystem: string; total_bytes: number; used_bytes: number; pct: number }
export interface NetStat { iface: string; rx_bytes: number; tx_bytes: number; rx_bps: number; tx_bps: number }
export interface TempStat { label: string; c: number }
export interface ProcStat { pid: number; user: string; cpu_pct: number; mem_pct: number; rss_kb: number; command: string }
export interface ServiceStat { name: string; state: string; description: string }
export interface ContainerStat { id: string; name: string; image: string; state: string; status: string }

export interface Snapshot {
  ts: number
  cpu_pct?: number
  load1?: number
  load5?: number
  load15?: number
  mem_total?: number
  mem_used?: number
  mem_pct?: number
  swap_total?: number
  swap_used?: number
  swap_pct?: number
  uptime_secs?: number
  battery_pct?: number
  latency_ms?: number
  disks?: DiskStat[]
  net?: NetStat[]
  temps?: TempStat[]
  processes?: ProcStat[]
  services?: ServiceStat[]
  containers?: ContainerStat[]
}

export type DeviceStatus = 'online' | 'offline' | 'unknown'

export interface DeviceState {
  device_id: number
  status: DeviceStatus
  last_seen: number
  last_attempt: number
  last_error?: string
  consecutive_failures: number
  poll_duration_ms: number
  interval_s: number
  started_at: number
  host_key_mismatch: boolean
  unsupported: boolean
  collector: string
  latest?: Snapshot
}

export type Severity = 'info' | 'warning' | 'critical'
export type AlertStateName = 'ok' | 'pending' | 'firing' | 'resolved' | 'unknown'

export interface AlertRule {
  id: number
  name: string
  device_id: number | null
  group: string | null
  device_type: DeviceType | null
  metric: string
  operator: 'gt' | 'lt' | 'eq'
  threshold: number
  duration_s: number
  resolve_threshold: number | null
  severity: Severity
  enabled: boolean
  cooldown_s: number
  created_at: number
  updated_at: number
}

export interface AlertState {
  id: number
  rule_id: number
  device_id: number
  state: AlertStateName
  value: number | null
  pending_since: number | null
  fired_at: number | null
  resolved_at: number | null
  last_eval_at: number
  acked_at: number | null
  muted_until: number | null
  suppressed: boolean
  rule_name: string
  device_name: string
  metric: string
  severity: Severity
}

export interface AlertHistory {
  id: number
  rule_name: string
  device_name: string
  metric: string
  severity: Severity
  peak_value: number | null
  fired_at: number
  resolved_at: number
  acked: boolean
}

export interface AuditEntry { id: number; ts: number; device_id: number | null; action: string; payload: string; source_ip: string }

export interface Page<T> { items: T[]; total: number; page: number; page_size: number }

export interface SSEEvent { type: string; data: any; ts: number }
