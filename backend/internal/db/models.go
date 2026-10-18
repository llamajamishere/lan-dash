package db

// Device is a monitored host or smart device.
type Device struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"` // 0 = collector default (22 for SSH)
	Type          string  `json:"type"` // ubuntu|windows|hue|blink|plug|ping
	Enabled       bool    `json:"enabled"`
	ParentID      *int64  `json:"parent_id"`
	Group         *string `json:"group"`
	Notes         string  `json:"notes"`
	PollIntervalS int     `json:"poll_interval_s"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

// ValidDeviceTypes lists the device types accepted by the API.
var ValidDeviceTypes = map[string]bool{
	"ubuntu": true, "windows": true, "hue": true, "blink": true, "plug": true, "ping": true,
}

// Credential is the decrypted form of a credentials row. It never leaves the
// server; the API only exposes CredentialInfo.
type Credential struct {
	DeviceID   int64
	Kind       string
	Username   string
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// CredentialInfo is the browser-safe view of a credential.
type CredentialInfo struct {
	DeviceID      int64  `json:"device_id"`
	Kind          string `json:"kind"`
	Username      string `json:"username"`
	HasPassword   bool   `json:"has_password"`
	HasPrivateKey bool   `json:"has_private_key"`
	UpdatedAt     int64  `json:"updated_at"`
}

// AlertRule defines a threshold check applied to matching devices.
type AlertRule struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	DeviceID         *int64   `json:"device_id"`   // nil = all devices
	Group            *string  `json:"group"`       // nil = all groups
	DeviceType       *string  `json:"device_type"` // nil = all types
	Metric           string   `json:"metric"`
	Operator         string   `json:"operator"` // gt|lt|eq
	Threshold        float64  `json:"threshold"`
	DurationS        int      `json:"duration_s"`
	ResolveThreshold *float64 `json:"resolve_threshold"`
	Severity         string   `json:"severity"` // info|warning|critical
	Enabled          bool     `json:"enabled"`
	CooldownS        int      `json:"cooldown_s"`
	CreatedAt        int64    `json:"created_at"`
	UpdatedAt        int64    `json:"updated_at"`
}

// Alert states.
const (
	StateOK       = "ok"
	StatePending  = "pending"
	StateFiring   = "firing"
	StateResolved = "resolved"
	StateUnknown  = "unknown"
)

// AlertState is the persisted state-machine row for one (rule, device).
type AlertState struct {
	ID             int64    `json:"id"`
	RuleID         int64    `json:"rule_id"`
	DeviceID       int64    `json:"device_id"`
	State          string   `json:"state"`
	Value          *float64 `json:"value"`
	PendingSince   *int64   `json:"pending_since"`
	FiredAt        *int64   `json:"fired_at"`
	ResolvedAt     *int64   `json:"resolved_at"`
	LastEvalAt     int64    `json:"last_eval_at"`
	AckedAt        *int64   `json:"acked_at"`
	MutedUntil     *int64   `json:"muted_until"`
	Suppressed     bool     `json:"suppressed"`
	LastNotifiedAt *int64   `json:"last_notified_at"`

	// Joined, read-only fields for the UI.
	RuleName   string `json:"rule_name,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Metric     string `json:"metric,omitempty"`
	Severity   string `json:"severity,omitempty"`
}

// AlertHistory is a resolved alert occurrence.
type AlertHistory struct {
	ID         int64    `json:"id"`
	RuleID     *int64   `json:"rule_id"`
	DeviceID   *int64   `json:"device_id"`
	RuleName   string   `json:"rule_name"`
	DeviceName string   `json:"device_name"`
	Metric     string   `json:"metric"`
	Severity   string   `json:"severity"`
	PeakValue  *float64 `json:"peak_value"`
	FiredAt    int64    `json:"fired_at"`
	ResolvedAt int64    `json:"resolved_at"`
	Acked      bool     `json:"acked"`
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID       int64  `json:"id"`
	Ts       int64  `json:"ts"`
	DeviceID *int64 `json:"device_id"`
	Action   string `json:"action"`
	Payload  string `json:"payload"`
	SourceIP string `json:"source_ip"`
}

// MetricPoint is one 1-minute aggregate.
type MetricPoint struct {
	Ts     int64   `json:"ts"`
	Metric string  `json:"metric"`
	Avg    float64 `json:"avg"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}
