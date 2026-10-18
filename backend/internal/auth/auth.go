// Package auth implements the v1 shared-password authentication:
// bcrypt password hash, signed session cookies backed by SQLite, CSRF
// double-submit tokens and a login rate limiter.
//
// Everything is exposed as middleware + a small Authenticator so per-user
// accounts can replace the shared password later without touching handlers.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/user/lan-dash/internal/db"
)

const (
	SessionCookie = "landash_session"
	CSRFCookie    = "landash_csrf"
	CSRFHeader    = "X-CSRF-Token"
	SessionTTL    = 24 * time.Hour

	cfgPasswordHash  = "auth_password_hash"
	cfgSessionSecret = "auth_session_secret"
	minPasswordLen   = 8
)

var (
	ErrBadPassword    = errors.New("invalid password")
	ErrAlreadySetup   = errors.New("password already set")
	ErrWeakPassword   = errors.New("password must be at least 8 characters")
	ErrRateLimited    = errors.New("too many login attempts; try again later")
)

// Authenticator owns password + session logic.
type Authenticator struct {
	db            *db.DB
	secret        []byte
	SecureCookies bool // set when served over HTTPS (reverse proxy)
	TrustProxy    bool // honour X-Forwarded-For for client IP
	Limiter       *RateLimiter

	mu        sync.RWMutex
	setupDone bool
}

// New loads (or creates) the session HMAC secret.
func New(ctx context.Context, d *db.DB) (*Authenticator, error) {
	a := &Authenticator{db: d, Limiter: NewRateLimiter(5, 15*time.Minute)}
	secHex, ok, err := d.GetConfig(ctx, cfgSessionSecret)
	if err != nil {
		return nil, err
	}
	if !ok {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		secHex = hex.EncodeToString(b)
		if err := d.SetConfig(ctx, cfgSessionSecret, secHex); err != nil {
			return nil, err
		}
	}
	if a.secret, err = hex.DecodeString(secHex); err != nil {
		return nil, err
	}
	_, a.setupDone, err = d.GetConfig(ctx, cfgPasswordHash)
	return a, err
}

// SetupRequired reports whether no password has been configured yet.
func (a *Authenticator) SetupRequired() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.setupDone
}

// Setup sets the initial password. Fails if one already exists.
func (a *Authenticator) Setup(ctx context.Context, password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.setupDone {
		return ErrAlreadySetup
	}
	if err := a.storePassword(ctx, password); err != nil {
		return err
	}
	a.setupDone = true
	return nil
}

// ChangePassword verifies current and stores next; all sessions are revoked.
func (a *Authenticator) ChangePassword(ctx context.Context, current, next string) error {
	if err := a.CheckPassword(ctx, current); err != nil {
		return err
	}
	if err := a.storePassword(ctx, next); err != nil {
		return err
	}
	return a.db.DeleteAllSessions(ctx)
}

func (a *Authenticator) storePassword(ctx context.Context, password string) error {
	if len(password) < minPasswordLen {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	return a.db.SetConfig(ctx, cfgPasswordHash, string(hash))
}

// CheckPassword compares against the stored bcrypt hash.
func (a *Authenticator) CheckPassword(ctx context.Context, password string) error {
	hash, ok, err := a.db.GetConfig(ctx, cfgPasswordHash)
	if err != nil {
		return err
	}
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrBadPassword
	}
	return nil
}

// ---------- sessions ----------

func (a *Authenticator) sign(token []byte) string {
	m := hmac.New(sha256.New, a.secret)
	m.Write(token)
	return base64.RawURLEncoding.EncodeToString(token) + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// verify checks the HMAC and returns the session's DB key (sha256 of token).
func (a *Authenticator) verify(cookie string) (string, bool) {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	token, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	sig, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	if err1 != nil || err2 != nil {
		return "", false
	}
	m := hmac.New(sha256.New, a.secret)
	m.Write(token)
	if !hmac.Equal(sig, m.Sum(nil)) {
		return "", false
	}
	h := sha256.Sum256(token)
	return hex.EncodeToString(h[:]), true
}

// StartSession creates a session and sets the session + CSRF cookies.
func (a *Authenticator) StartSession(w http.ResponseWriter, r *http.Request) error {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	h := sha256.Sum256(token)
	if err := a.db.CreateSession(r.Context(), hex.EncodeToString(h[:]), a.ClientIP(r), SessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: a.sign(token), Path: "/", HttpOnly: true,
		Secure: a.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(SessionTTL.Seconds()),
	})
	a.setCSRFCookie(w) // rotate CSRF token on login
	return nil
}

// EndSession deletes the current session and clears the cookie.
func (a *Authenticator) EndSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if key, ok := a.verify(c.Value); ok {
			_ = a.db.DeleteSession(r.Context(), key)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.SecureCookies})
}

// Authenticated reports whether r carries a valid session cookie.
func (a *Authenticator) Authenticated(r *http.Request) bool {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return false
	}
	key, ok := a.verify(c.Value)
	if !ok {
		return false
	}
	valid, err := a.db.SessionValid(r.Context(), key)
	return err == nil && valid
}

// ---------- CSRF (double-submit cookie) ----------

func (a *Authenticator) setCSRFCookie(w http.ResponseWriter) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	// Readable by JS (not HttpOnly) so the SPA can echo it in a header.
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: tok, Path: "/",
		Secure: a.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(SessionTTL.Seconds()) * 7})
	return tok
}

// EnsureCSRFCookie issues a CSRF cookie if the client has none.
func (a *Authenticator) EnsureCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CSRFCookie); err == nil && len(c.Value) >= 32 {
		return
	}
	a.setCSRFCookie(w)
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// CSRFMiddleware rejects mutating requests whose X-CSRF-Token header does
// not match the CSRF cookie, or whose Origin is a different host.
func (a *Authenticator) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMutating(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					jsonError(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
			c, err := r.Cookie(CSRFCookie)
			hdr := r.Header.Get(CSRFHeader)
			if err != nil || hdr == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(hdr)) != 1 {
				jsonError(w, http.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth returns 428 if setup is pending and 401 without a session.
func (a *Authenticator) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.SetupRequired() {
			jsonError(w, http.StatusPreconditionRequired, "setup required")
			return
		}
		if !a.Authenticated(r) {
			jsonError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP returns the caller's IP address.
func (a *Authenticator) ClientIP(r *http.Request) string {
	if a.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
