package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/user/lan-dash/internal/auth"
)

type passwordReq struct {
	Password string `json:"password"`
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	s.Auth.EnsureCSRFCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required": s.Auth.SetupRequired(),
		"authenticated":  !s.Auth.SetupRequired() && s.Auth.Authenticated(r),
		"version":        s.Version,
	})
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	var req passwordReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.Auth.Setup(r.Context(), req.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrAlreadySetup):
			writeErr(w, http.StatusConflict, err.Error())
		case errors.Is(err, auth.ErrWeakPassword):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeDBErr(w, err)
		}
		return
	}
	s.audit(r, nil, "auth:setup", map[string]any{})
	slog.Info("initial password set", "ip", s.Auth.ClientIP(r))
	if err := s.Auth.StartSession(w, r); err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	ip := s.Auth.ClientIP(r)
	if !s.Auth.Limiter.Allow(ip) {
		slog.Warn("login rate limited", "ip", ip)
		writeErr(w, http.StatusTooManyRequests, auth.ErrRateLimited.Error())
		return
	}
	var req passwordReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if s.Auth.SetupRequired() {
		writeErr(w, http.StatusPreconditionRequired, "setup required")
		return
	}
	if err := s.Auth.CheckPassword(r.Context(), req.Password); err != nil {
		slog.Warn("login failed", "ip", ip)
		s.audit(r, nil, "auth:login_failed", map[string]any{})
		writeErr(w, http.StatusUnauthorized, "invalid password")
		return
	}
	s.Auth.Limiter.Reset(ip)
	if err := s.Auth.StartSession(w, r); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, nil, "auth:login", map[string]any{})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	s.Auth.EndSession(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ip := s.Auth.ClientIP(r)
	if !s.Auth.Limiter.Allow(ip) {
		writeErr(w, http.StatusTooManyRequests, auth.ErrRateLimited.Error())
		return
	}
	if err := s.Auth.ChangePassword(r.Context(), req.Current, req.New); err != nil {
		switch {
		case errors.Is(err, auth.ErrBadPassword):
			writeErr(w, http.StatusUnauthorized, "current password is incorrect")
		case errors.Is(err, auth.ErrWeakPassword):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeDBErr(w, err)
		}
		return
	}
	s.Auth.Limiter.Reset(ip)
	s.audit(r, nil, "auth:password_changed", map[string]any{})
	// All sessions were revoked; give this browser a fresh one.
	if err := s.Auth.StartSession(w, r); err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
