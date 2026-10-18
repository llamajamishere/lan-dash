package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/user/lan-dash/internal/collector"
	"github.com/user/lan-dash/internal/db"
	"github.com/user/lan-dash/internal/sse"
)

type deviceReq struct {
	Name          string  `json:"name"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Type          string  `json:"type"`
	Enabled       *bool   `json:"enabled"`
	ParentID      *int64  `json:"parent_id"`
	Group         *string `json:"group"`
	Notes         string  `json:"notes"`
	PollIntervalS int     `json:"poll_interval_s"`
}

func (s *Server) validateDevice(ctx context.Context, req *deviceReq, selfID int64) error {
	req.Name, req.Host = strings.TrimSpace(req.Name), strings.TrimSpace(req.Host)
	switch {
	case req.Name == "" || len(req.Name) > 100:
		return errors.New("name is required (max 100 chars)")
	case req.Host == "" || len(req.Host) > 255 || strings.ContainsAny(req.Host, " /\\@"):
		return errors.New("host must be a hostname or IP address")
	case !db.ValidDeviceTypes[req.Type]:
		return errors.New("type must be one of ubuntu, windows, hue, blink, plug, ping")
	case req.Port < 0 || req.Port > 65535:
		return errors.New("port out of range")
	}
	if req.PollIntervalS == 0 {
		req.PollIntervalS = 15
		if req.Type == "windows" {
			req.PollIntervalS = 30
		}
	}
	if req.PollIntervalS < 5 || req.PollIntervalS > 3600 {
		return errors.New("poll_interval_s must be between 5 and 3600")
	}
	if req.ParentID != nil {
		if *req.ParentID == selfID {
			return errors.New("a device cannot be its own parent")
		}
		// Walk up the chain to reject cycles.
		cur := *req.ParentID
		for i := 0; i < 32; i++ {
			p, err := s.DB.GetDevice(ctx, cur)
			if err != nil {
				return errors.New("parent device not found")
			}
			if p.ParentID == nil {
				break
			}
			if *p.ParentID == selfID {
				return errors.New("parent relationship would create a cycle")
			}
			cur = *p.ParentID
		}
	}
	if req.Group != nil {
		g := strings.TrimSpace(*req.Group)
		if g == "" {
			req.Group = nil
		} else {
			req.Group = &g
		}
	}
	return nil
}

func (req *deviceReq) apply(d *db.Device) {
	d.Name, d.Host, d.Port, d.Type = req.Name, req.Host, req.Port, req.Type
	d.ParentID, d.Group, d.Notes, d.PollIntervalS = req.ParentID, req.Group, req.Notes, req.PollIntervalS
	if req.Enabled != nil {
		d.Enabled = *req.Enabled
	}
}

func (s *Server) reloadCollectors(ctx context.Context) {
	if err := s.Manager.Reload(ctx); err != nil {
		writeLog("collector reload", err)
	}
	s.Hub.Publish(sse.EventDevicesChanged, map[string]any{})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := s.DB.ListDevices(r.Context())
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, devs)
}

func (s *Server) getDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.DB.GetDevice(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.validateDevice(r.Context(), &req, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	d := &db.Device{Enabled: true}
	req.apply(d)
	if err := s.DB.CreateDevice(r.Context(), d); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, &d.ID, "device:create", map[string]any{"name": d.Name, "host": d.Host, "type": d.Type})
	s.reloadCollectors(r.Context())
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.DB.GetDevice(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	var req deviceReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.validateDevice(r.Context(), &req, id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.apply(d)
	if err := s.DB.UpdateDevice(r.Context(), d); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, &d.ID, "device:update", map[string]any{"name": d.Name, "host": d.Host, "type": d.Type, "enabled": d.Enabled})
	s.reloadCollectors(r.Context())
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.DB.GetDevice(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	if err := s.DB.DeleteDevice(r.Context(), id); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, nil, "device:delete", map[string]any{"id": id, "name": d.Name})
	s.reloadCollectors(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// ---------- credentials (write-only) ----------

func (s *Server) getCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	info, err := s.Creds.Info(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"device_id": id, "configured": false})
		return
	}
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "device_id": info.DeviceID, "kind": info.Kind,
		"username": info.Username, "has_password": info.HasPassword, "has_private_key": info.HasPrivateKey, "updated_at": info.UpdatedAt})
}

func (s *Server) putCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.DB.GetDevice(r.Context(), id); err != nil {
		writeDBErr(w, err)
		return
	}
	var req struct {
		Kind       string `json:"kind"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		PrivateKey string `json:"private_key"`
		Passphrase string `json:"passphrase"`
		Clear      bool   `json:"clear"` // replace instead of merging blank fields
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	cred := &db.Credential{DeviceID: id, Kind: req.Kind, Username: strings.TrimSpace(req.Username),
		Password: req.Password, PrivateKey: strings.TrimSpace(req.PrivateKey), Passphrase: req.Passphrase}
	if cred.PrivateKey != "" {
		cred.PrivateKey += "\n"
	}
	if err := s.Creds.Put(r.Context(), cred, req.Clear); err != nil {
		writeDBErr(w, err)
		return
	}
	// Never log or audit secret values — only which fields were set.
	s.audit(r, &id, "credential:update", map[string]any{"username": cred.Username,
		"password_set": req.Password != "", "private_key_set": req.PrivateKey != ""})
	s.reloadCollectorsForce(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Creds.Delete(r.Context(), id); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, &id, "credential:delete", map[string]any{})
	s.reloadCollectorsForce(r.Context(), id)
	w.WriteHeader(http.StatusNoContent)
}

// reloadCollectorsForce bumps updated_at so the device's worker restarts
// and reconnects with the new credentials.
func (s *Server) reloadCollectorsForce(ctx context.Context, id int64) {
	if d, err := s.DB.GetDevice(ctx, id); err == nil {
		_ = s.DB.UpdateDevice(ctx, d)
	}
	s.reloadCollectors(ctx)
}

func (s *Server) resetHostKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.DB.GetDevice(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	if err := s.Manager.ResetHostKey(r.Context(), d); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, &id, "device:hostkey_reset", map[string]any{"host": d.Host})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- control & live data ----------

func (s *Server) controlDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.DB.GetDevice(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	var action collector.Action
	if err := readJSON(r, &action); err != nil || action.Name == "" {
		writeErr(w, http.StatusBadRequest, "body must be {\"name\": \"...\", \"params\": {...}}")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.Manager.Control(ctx, d, action, s.Auth.ClientIP(r)); err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, collector.ErrUnsupportedAction) || errors.Is(err, collector.ErrNotImplemented) {
			code = http.StatusBadRequest
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deviceSnapshots(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	secs := queryInt(r, "seconds", 300, 10, 3600)
	since := time.Now().Unix() - int64(secs)
	snaps := s.Manager.Snapshots(id, since)
	// Optional "lite" mode strips heavy lists for sparkline bootstrap.
	if r.URL.Query().Get("lite") == "1" {
		lite := make([]map[string]any, 0, len(snaps))
		for _, sn := range snaps {
			rx, tx, _ := sn.NetTotals()
			lite = append(lite, map[string]any{"ts": sn.Ts, "cpu_pct": sn.CPUPct, "mem_pct": sn.MemPct, "net_rx_bps": rx, "net_tx_bps": tx})
		}
		writeJSON(w, http.StatusOK, lite)
		return
	}
	writeJSON(w, http.StatusOK, snaps)
}

func (s *Server) deviceMetrics1m(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	hours := queryInt(r, "hours", 24, 1, 24)
	pts, err := s.DB.ListMetrics1m(r.Context(), id, time.Now().Add(-time.Duration(hours)*time.Hour).Unix())
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pts)
}

func (s *Server) allStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Manager.States())
}

func writeLog(what string, err error) { slog.Error(what, "err", err) }
