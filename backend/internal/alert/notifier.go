package alert

import (
	"context"
	"log/slog"

	"github.com/user/lan-dash/internal/db"
)

// Notifier delivers alert transitions to an external channel. v1 only shows
// alerts in the UI; ntfy / Telegram / Discord / webhook notifiers will
// implement this interface in phase 8. alert.State is "firing" or
// "resolved". Muted alerts are never passed to notifiers.
type Notifier interface {
	Name() string
	Notify(ctx context.Context, alert db.AlertState) error
}

// NoopNotifier logs at debug level and does nothing else.
type NoopNotifier struct{}

// Name implements Notifier.
func (NoopNotifier) Name() string { return "noop" }

// Notify implements Notifier.
func (NoopNotifier) Notify(_ context.Context, a db.AlertState) error {
	slog.Debug("notify (noop)", "rule", a.RuleName, "device", a.DeviceName, "state", a.State)
	return nil
}

// MultiNotifier fans out to several notifiers, logging failures.
type MultiNotifier []Notifier

// Name implements Notifier.
func (MultiNotifier) Name() string { return "multi" }

// Notify implements Notifier.
func (m MultiNotifier) Notify(ctx context.Context, a db.AlertState) error {
	for _, n := range m {
		if err := n.Notify(ctx, a); err != nil {
			slog.Error("notifier failed", "notifier", n.Name(), "err", err)
		}
	}
	return nil
}
