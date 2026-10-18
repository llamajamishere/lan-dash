package api

import (
	"net/http"
	"runtime"
	"time"
)

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"mock_enabled": s.Manager.MockEnabled()})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MockEnabled *bool `json:"mock_enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.MockEnabled != nil {
		if err := s.Manager.SetMock(r.Context(), *req.MockEnabled); err != nil {
			writeDBErr(w, err)
			return
		}
		s.audit(r, nil, "settings:mock_enabled", map[string]any{"value": *req.MockEnabled})
	}
	s.getSettings(w, r)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        s.Version,
		"started_at":     s.StartedAt.Unix(),
		"uptime_secs":    int64(time.Since(s.StartedAt).Seconds()),
		"db_size_bytes":  s.DB.SizeBytes(),
		"schema_version": s.DB.SchemaVersion(r.Context()),
		"go_version":     runtime.Version(),
		"goroutines":     runtime.NumGoroutine(),
		"heap_bytes":     mem.HeapAlloc,
		"sse_clients":    s.Hub.ClientCount(),
		"mock_enabled":   s.Manager.MockEnabled(),
		"collectors":     s.Manager.States(),
	})
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1, 1, 100000)
	size := queryInt(r, "page_size", 50, 1, 200)
	items, total, err := s.DB.ListAudit(r.Context(), size, (page-1)*size)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
