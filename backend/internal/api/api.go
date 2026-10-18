// Package api wires HTTP routes (Go 1.22 net/http pattern routing) to the
// application services, and serves the embedded SPA.
package api

import (
        "encoding/json"
        "errors"
        "io/fs"
        "log/slog"
        "net/http"
        "path"
        "strconv"
        "strings"
        "time"

        "github.com/user/lan-dash/internal/alert"
        "github.com/user/lan-dash/internal/auth"
        "github.com/user/lan-dash/internal/collector"
        "github.com/user/lan-dash/internal/db"
        "github.com/user/lan-dash/internal/secrets"
        "github.com/user/lan-dash/internal/sse"
)

// Server holds dependencies for handlers.
type Server struct {
        DB        *db.DB
        Auth      *auth.Authenticator
        Creds     *secrets.CredentialStore
        Manager   *collector.Manager
        Alerts    *alert.Engine
        Hub       *sse.Hub
        Static    fs.FS // built SPA (index.html + assets)
        Version   string
        StartedAt time.Time
}

// Handler builds the full HTTP handler.
func (s *Server) Handler() http.Handler {
        public := http.NewServeMux()
        public.HandleFunc("GET /api/auth/status", s.authStatus)
        public.HandleFunc("POST /api/auth/setup", s.authSetup)
        public.HandleFunc("POST /api/auth/login", s.authLogin)
        public.HandleFunc("POST /api/auth/logout", s.authLogout)
        public.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
                writeJSON(w, http.StatusOK, map[string]string{"version": s.Version})
        })

        priv := http.NewServeMux()
        priv.HandleFunc("POST /api/auth/password", s.authChangePassword)

        priv.HandleFunc("GET /api/devices", s.listDevices)
        priv.HandleFunc("POST /api/devices", s.createDevice)
        priv.HandleFunc("GET /api/devices/{id}", s.getDevice)
        priv.HandleFunc("PUT /api/devices/{id}", s.updateDevice)
        priv.HandleFunc("DELETE /api/devices/{id}", s.deleteDevice)
        priv.HandleFunc("GET /api/devices/{id}/credentials", s.getCredential)
        priv.HandleFunc("PUT /api/devices/{id}/credentials", s.putCredential)
        priv.HandleFunc("DELETE /api/devices/{id}/credentials", s.deleteCredential)
        priv.HandleFunc("POST /api/devices/{id}/hostkey/reset", s.resetHostKey)
        priv.HandleFunc("POST /api/devices/{id}/control", s.controlDevice)
        priv.HandleFunc("GET /api/devices/{id}/snapshots", s.deviceSnapshots)
        priv.HandleFunc("GET /api/devices/{id}/metrics", s.deviceMetrics1m)

        priv.HandleFunc("GET /api/status", s.allStatus)
        priv.Handle("GET /api/events", s.Hub)

        priv.HandleFunc("GET /api/alerts", s.activeAlerts)
        priv.HandleFunc("GET /api/alerts/history", s.alertHistory)
        priv.HandleFunc("POST /api/alerts/{id}/ack", s.alertAction(alert.ActionAck))
        priv.HandleFunc("POST /api/alerts/{id}/mute", s.alertAction(alert.ActionMute))
        priv.HandleFunc("POST /api/alerts/{id}/unmute", s.alertAction(alert.ActionUnmute))
        priv.HandleFunc("GET /api/alert-rules", s.listRules)
        priv.HandleFunc("POST /api/alert-rules", s.createRule)
        priv.HandleFunc("PUT /api/alert-rules/{id}", s.updateRule)
        priv.HandleFunc("DELETE /api/alert-rules/{id}", s.deleteRule)
        priv.HandleFunc("GET /api/alert-metrics", func(w http.ResponseWriter, r *http.Request) {
                writeJSON(w, http.StatusOK, alert.Metrics)
        })

        priv.HandleFunc("GET /api/settings", s.getSettings)
        priv.HandleFunc("PUT /api/settings", s.putSettings)
        priv.HandleFunc("GET /api/health", s.health)
        priv.HandleFunc("GET /api/audit", s.auditLog)

        privH := s.Auth.RequireAuth(priv)

        root := http.NewServeMux()
        root.Handle("/api/auth/status", public)
        root.Handle("/api/auth/setup", public)
        root.Handle("/api/auth/login", public)
        root.Handle("/api/auth/logout", public)
        root.Handle("/api/version", public)
        root.Handle("/api/", privH)
        root.HandleFunc("/", s.spa)

        return logRequests(securityHeaders(s.Auth.CSRFMiddleware(root)))
}

// spa serves static assets and falls back to index.html for client routes.
// Before setup it redirects every page to /setup, and away from it after.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodGet && r.Method != http.MethodHead {
                http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
                return
        }
        p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
        // Note: path.Base("") is ".", so the root path must be excluded explicitly.
        isAsset := p != "" && (strings.HasPrefix(p, "assets/") || strings.Contains(path.Base(p), "."))
        if !isAsset {
                if s.Auth.SetupRequired() && p != "setup" {
                        http.Redirect(w, r, "/setup", http.StatusFound)
                        return
                }
                if !s.Auth.SetupRequired() && p == "setup" {
                        http.Redirect(w, r, "/login", http.StatusFound)
                        return
                }
        }
        s.Auth.EnsureCSRFCookie(w, r)
        if p != "" && isAsset {
                if f, err := s.Static.Open(p); err == nil {
                        f.Close()
                        if strings.HasPrefix(p, "assets/") {
                                w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
                        }
                        http.ServeFileFS(w, r, s.Static, p)
                        return
                }
                http.NotFound(w, r)
                return
        }
        idx, err := fs.ReadFile(s.Static, "index.html")
        if err != nil {
                w.Header().Set("Content-Type", "text/html; charset=utf-8")
                w.WriteHeader(http.StatusServiceUnavailable)
                _, _ = w.Write([]byte("<h1>LAN Dash</h1><p>Frontend not built. Run <code>./build.sh</code> (or <code>make dev</code> and open the Vite dev server).</p>"))
                return
        }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        w.Header().Set("Cache-Control", "no-cache")
        _, _ = w.Write(idx)
}

// ---------- middleware ----------

func securityHeaders(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                h := w.Header()
                h.Set("X-Content-Type-Options", "nosniff")
                h.Set("X-Frame-Options", "DENY")
                h.Set("Referrer-Policy", "same-origin")
                h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
                next.ServeHTTP(w, r)
        })
}

type statusRecorder struct {
        http.ResponseWriter
        code int
}

func (s *statusRecorder) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Flush keeps SSE working through the recorder.
func (s *statusRecorder) Flush() {
        if f, ok := s.ResponseWriter.(http.Flusher); ok {
                f.Flush()
        }
}

func logRequests(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                start := time.Now()
                rec := &statusRecorder{ResponseWriter: w, code: 200}
                next.ServeHTTP(rec, r)
                if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/events" {
                        slog.Debug("http", "method", r.Method, "path", r.URL.Path, "status", rec.code, "ms", time.Since(start).Milliseconds())
                }
        })
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(code)
        _ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
        writeJSON(w, code, map[string]string{"error": msg})
}

// writeDBErr maps db errors to HTTP codes without leaking internals.
func writeDBErr(w http.ResponseWriter, err error) {
        if errors.Is(err, db.ErrNotFound) {
                writeErr(w, http.StatusNotFound, "not found")
                return
        }
        slog.Error("handler error", "err", err)
        writeErr(w, http.StatusInternalServerError, "internal error")
}

func readJSON(r *http.Request, v any) error {
        r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
        dec := json.NewDecoder(r.Body)
        return dec.Decode(v)
}

func pathID(r *http.Request) (int64, bool) {
        id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
        return id, err == nil && id > 0
}

func queryInt(r *http.Request, name string, def, lo, hi int) int {
        v, err := strconv.Atoi(r.URL.Query().Get(name))
        if err != nil {
                return def
        }
        return max(lo, min(hi, v))
}

func (s *Server) audit(r *http.Request, deviceID *int64, action string, payload any) {
        b, _ := json.Marshal(payload)
        if err := s.DB.InsertAudit(r.Context(), deviceID, action, string(b), s.Auth.ClientIP(r)); err != nil {
                slog.Error("audit", "err", err)
        }
}
