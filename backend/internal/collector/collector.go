// Package collector defines the plugin interface every device type
// implements, the metric Snapshot model, and the Manager that schedules
// polling. Adding a new device type = implement Collector + Register it.
package collector

import (
	"context"
	"errors"
	"fmt"

	"github.com/user/lan-dash/internal/db"
)

// Collector is implemented by every device-type plugin.
type Collector interface {
	// Type returns the device type this collector handles (e.g. "ubuntu").
	Type() string
	// Discover validates connectivity / fills in device facts. Optional
	// work; may return nil immediately.
	Discover(ctx context.Context, dev *db.Device) error
	// Poll gathers one snapshot. Return an error wrapped with Unreachable()
	// when the device cannot be contacted at all (status=offline); any other
	// error yields status=unknown.
	Poll(ctx context.Context, dev *db.Device) (*Snapshot, error)
	// Control performs an action (toggle light, restart service...).
	Control(ctx context.Context, dev *db.Device, action Action) error
}

// Releaser is optionally implemented by collectors that hold per-device
// resources (e.g. persistent SSH connections) to free when polling stops.
type Releaser interface {
	Release(deviceID int64)
}

// Action is a control request.
type Action struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// ---------- errors ----------

// ErrUnreachable marks errors meaning "device is down / not reachable".
var ErrUnreachable = errors.New("device unreachable")

// ErrNotImplemented is returned by stub collectors for later phases.
var ErrNotImplemented = errors.New("collector not implemented yet")

// ErrUnsupportedAction is returned by Control for unknown actions.
var ErrUnsupportedAction = errors.New("unsupported action")

// Unreachable wraps err so errors.Is(err, ErrUnreachable) is true.
func Unreachable(err error) error { return fmt.Errorf("%w: %v", ErrUnreachable, err) }

// HostKeyMismatchError is returned when a TOFU host key check fails.
type HostKeyMismatchError struct {
	HostPort string
	Expected string
	Got      string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("SSH host key for %s changed (expected %s, got %s) - possible MITM; reset the stored key if this change is expected",
		e.HostPort, e.Expected, e.Got)
}

// ---------- dependencies supplied by main ----------

// CredentialGetter returns decrypted credentials for a device.
type CredentialGetter interface {
	Get(ctx context.Context, deviceID int64) (*db.Credential, error)
}

// KV is a small persistent key/value store (backed by the config table)
// used for TOFU host-key fingerprints.
type KV interface {
	GetConfig(ctx context.Context, key string) (string, bool, error)
	SetConfig(ctx context.Context, key, value string) error
	DeleteConfig(ctx context.Context, key string) error
}

// ---------- registry ----------

// Registry maps device types to collectors.
type Registry struct{ m map[string]Collector }

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return &Registry{m: map[string]Collector{}} }

// Register adds a collector for its Type().
func (r *Registry) Register(c Collector) { r.m[c.Type()] = c }

// Get returns the collector for a device type.
func (r *Registry) Get(t string) (Collector, bool) {
	c, ok := r.m[t]
	return c, ok
}

// All returns every registered collector.
func (r *Registry) All() []Collector {
	out := make([]Collector, 0, len(r.m))
	for _, c := range r.m {
		out = append(out, c)
	}
	return out
}
