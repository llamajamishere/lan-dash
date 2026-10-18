// Command lan-dash is the self-hosted LAN monitoring dashboard server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	landash "github.com/user/lan-dash"
	"github.com/user/lan-dash/internal/alert"
	"github.com/user/lan-dash/internal/api"
	"github.com/user/lan-dash/internal/auth"
	"github.com/user/lan-dash/internal/collector"
	"github.com/user/lan-dash/internal/db"
	"github.com/user/lan-dash/internal/secrets"
	"github.com/user/lan-dash/internal/sse"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var (
		listen      = flag.String("listen", ":8080", "HTTP listen address")
		dbPath      = flag.String("db", "/var/lib/lan-dash/landash.db", "SQLite database path")
		keyPath     = flag.String("key", "/etc/lan-dash/master.key", "master key file (64 hex chars, mode 0600)")
		backupDir   = flag.String("backup-dir", "", "directory for automatic pre-migration backups (default: <db dir>/backups)")
		initKey     = flag.Bool("init-key", false, "generate the master key if missing (development only)")
		mock        = flag.Bool("mock", false, "force mock collectors on (sets mock_enabled=true)")
		seedDemo    = flag.Bool("seed-demo", false, "insert demo devices if the device table is empty")
		logLevel    = flag.String("log-level", "info", "debug|info|warn|error")
		logJSON     = flag.Bool("log-json", false, "log in JSON instead of logfmt")
		secure      = flag.Bool("secure-cookies", false, "set Secure on cookies (enable when behind an HTTPS reverse proxy)")
		trustProxy  = flag.Bool("trust-proxy", false, "trust X-Forwarded-For for client IPs (only behind a reverse proxy)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	setupLogging(*logLevel, *logJSON)
	if err := run(*listen, *dbPath, *keyPath, *backupDir, *initKey, *mock, *seedDemo, *secure, *trustProxy); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// setupLogging writes structured logs to stderr; under systemd they go to
// journald (view with `journalctl -u lan-dash -f`).
func setupLogging(level string, asJSON bool) {
	var lvl slog.Level
	_ = lvl.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: lvl}
	// journald adds its own timestamps.
	if os.Getenv("INVOCATION_ID") != "" {
		opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{}
			}
			return a
		}
	}
	var h slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if asJSON {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func run(listen, dbPath, keyPath, backupDir string, initKey, mock, seedDemo, secure, trustProxy bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("starting lan-dash", "version", version, "db", dbPath, "listen", listen)

	// --- master key ---
	if initKey {
		if err := secrets.GenerateMasterKey(keyPath); err != nil {
			return fmt.Errorf("generate key: %w", err)
		}
	}
	key, err := secrets.LoadMasterKey(keyPath)
	if err != nil {
		return err
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return err
	}

	// --- database: backup, then migrate ---
	database, err := db.Open(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	if backupDir == "" {
		backupDir = filepath.Join(filepath.Dir(dbPath), "backups")
	}
	if path, err := database.Backup(ctx, backupDir, 10); err != nil {
		return fmt.Errorf("pre-migration backup failed (refusing to migrate): %w", err)
	} else if path != "" {
		slog.Info("database backed up", "path", path)
	}
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	if mock {
		if err := database.SetConfig(ctx, collector.ConfigMockEnabled, "true"); err != nil {
			return err
		}
	}
	if seedDemo {
		if err := seedDemoDevices(ctx, database); err != nil {
			return err
		}
	}

	// --- services ---
	hub := sse.NewHub()
	creds := secrets.NewCredentialStore(database, box)
	authn, err := auth.New(ctx, database)
	if err != nil {
		return err
	}
	authn.SecureCookies, authn.TrustProxy = secure, trustProxy

	reg := collector.NewRegistry()
	reg.Register(collector.NewUbuntuCollector(creds, database))
	// Device types implemented in later phases.
	for _, t := range []string{"windows", "hue", "blink", "plug", "ping"} {
		reg.Register(collector.StubCollector{T: t})
	}
	mgr := collector.NewManager(database, reg, hub)
	if err := mgr.Start(ctx); err != nil {
		return err
	}
	engine := alert.NewEngine(database, mgr, hub, alert.NoopNotifier{})
	if err := engine.Start(ctx); err != nil {
		return err
	}

	go func() { // periodic rate-limiter cleanup
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				authn.Limiter.Cleanup()
			}
		}
	}()

	srv := &api.Server{DB: database, Auth: authn, Creds: creds, Manager: mgr, Alerts: engine, Hub: hub,
		Static: landash.StaticFS(), Version: version, StartedAt: time.Now()}
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE streams are long-lived.
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http listening", "addr", listen, "setup_required", authn.SetupRequired())
		errCh <- httpSrv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	slog.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shCtx)
	return nil
}

// seedDemoDevices adds sample devices for mock mode (only if none exist).
func seedDemoDevices(ctx context.Context, d *db.DB) error {
	devs, err := d.ListDevices(ctx)
	if err != nil || len(devs) > 0 {
		return err
	}
	g := func(s string) *string { return &s }
	router := &db.Device{Name: "Core switch", Host: "192.168.1.2", Type: "ping", Enabled: true, Group: g("network"), PollIntervalS: 15}
	if err := d.CreateDevice(ctx, router); err != nil {
		return err
	}
	demo := []*db.Device{
		{Name: "nas-01", Host: "192.168.1.10", Type: "ubuntu", Group: g("servers"), Notes: "Media + backups"},
		{Name: "docker-host", Host: "192.168.1.11", Type: "ubuntu", Group: g("servers")},
		{Name: "build-box", Host: "hot-build.lan", Type: "ubuntu", Group: g("servers"), Notes: "mock: runs hot"},
		{Name: "archive", Host: "full-archive.lan", Type: "ubuntu", Group: g("servers"), Notes: "mock: disk nearly full"},
		{Name: "pi-hole", Host: "flaky-pihole.lan", Type: "ubuntu", Group: g("network"), ParentID: &router.ID, Notes: "mock: drops out periodically"},
		{Name: "old-laptop", Host: "offline-laptop.lan", Type: "ubuntu", Notes: "mock: always offline"},
		{Name: "Hue Bridge", Host: "192.168.1.30", Type: "hue", Group: g("smart-home"), ParentID: &router.ID},
	}
	for _, dv := range demo {
		dv.Enabled = true
		if dv.PollIntervalS == 0 {
			dv.PollIntervalS = 5
		}
		if err := d.CreateDevice(ctx, dv); err != nil {
			return err
		}
	}
	slog.Info("seeded demo devices", "count", len(demo)+1, "hint", strings.Repeat("-", 3)+" enable mock mode to see data")
	return nil
}
