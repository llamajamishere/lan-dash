package api

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/user/lan-dash/internal/alert"
	"github.com/user/lan-dash/internal/db"
)

func (s *Server) activeAlerts(w http.ResponseWriter, r *http.Request) {
	states, err := s.DB.ListAlertStates(r.Context(), db.StateFiring, db.StatePending)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, states)
}

func (s *Server) alertHistory(w http.ResponseWriter, r *http.Request) {
	page := queryInt(r, "page", 1, 1, 100000)
	size := queryInt(r, "page_size", 25, 1, 200)
	items, total, err := s.DB.ListAlertHistory(r.Context(), size, (page-1)*size)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (s *Server) alertAction(action alert.Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid id")
			return
		}
		var req struct {
			DurationS int64 `json:"duration_s"`
		}
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeErr(w, http.StatusBadRequest, "invalid JSON")
				return
			}
		}
		if req.DurationS < 0 {
			writeErr(w, http.StatusBadRequest, "duration_s must be >= 0")
			return
		}
		st, err := s.Alerts.Apply(r.Context(), id, action, req.DurationS, s.Auth.ClientIP(r))
		if errors.Is(err, alert.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "alert not found")
			return
		}
		if err != nil {
			writeDBErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}

// ---------- rules ----------

func validateRule(r *db.AlertRule) error {
	r.Name = strings.TrimSpace(r.Name)
	switch {
	case r.Name == "" || len(r.Name) > 120:
		return errors.New("name is required (max 120 chars)")
	case !slices.Contains(alert.Metrics, r.Metric):
		return errors.New("unknown metric")
	case r.Operator != "gt" && r.Operator != "lt" && r.Operator != "eq":
		return errors.New("operator must be gt, lt or eq")
	case r.Severity != "info" && r.Severity != "warning" && r.Severity != "critical":
		return errors.New("severity must be info, warning or critical")
	case r.DurationS < 0 || r.DurationS > 86400:
		return errors.New("duration_s must be between 0 and 86400")
	case r.CooldownS < 0 || r.CooldownS > 86400:
		return errors.New("cooldown_s must be between 0 and 86400")
	}
	if r.ResolveThreshold != nil {
		rt := *r.ResolveThreshold
		if (r.Operator == "gt" && rt > r.Threshold) || (r.Operator == "lt" && rt < r.Threshold) {
			return errors.New("resolve_threshold must be on the healthy side of threshold")
		}
	}
	for _, p := range []**string{&r.Group, &r.DeviceType} {
		if *p != nil && strings.TrimSpace(**p) == "" {
			*p = nil
		}
	}
	if r.DeviceType != nil && !db.ValidDeviceTypes[*r.DeviceType] {
		return errors.New("invalid device_type")
	}
	return nil
}

func (s *Server) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.DB.ListAlertRules(r.Context())
	if err != nil {
		writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

type ruleReq struct {
	Name             string   `json:"name"`
	DeviceID         *int64   `json:"device_id"`
	Group            *string  `json:"group"`
	DeviceType       *string  `json:"device_type"`
	Metric           string   `json:"metric"`
	Operator         string   `json:"operator"`
	Threshold        float64  `json:"threshold"`
	DurationS        int      `json:"duration_s"`
	ResolveThreshold *float64 `json:"resolve_threshold"`
	Severity         string   `json:"severity"`
	Enabled          *bool    `json:"enabled"`
	CooldownS        int      `json:"cooldown_s"`
}

func (q *ruleReq) apply(r *db.AlertRule) {
	r.Name, r.DeviceID, r.Group, r.DeviceType = q.Name, q.DeviceID, q.Group, q.DeviceType
	r.Metric, r.Operator, r.Threshold, r.DurationS = q.Metric, q.Operator, q.Threshold, q.DurationS
	r.ResolveThreshold, r.Severity, r.CooldownS = q.ResolveThreshold, q.Severity, q.CooldownS
	if q.Enabled != nil {
		r.Enabled = *q.Enabled
	}
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	var req ruleReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	rule := &db.AlertRule{Enabled: true}
	req.apply(rule)
	if err := validateRule(rule); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.DB.CreateAlertRule(r.Context(), rule); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, nil, "alert_rule:create", rule)
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	rule, err := s.DB.GetAlertRule(r.Context(), id)
	if err != nil {
		writeDBErr(w, err)
		return
	}
	var req ruleReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	req.apply(rule)
	if err := validateRule(rule); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.DB.UpdateAlertRule(r.Context(), rule); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, nil, "alert_rule:update", rule)
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.DB.DeleteAlertRule(r.Context(), id); err != nil {
		writeDBErr(w, err)
		return
	}
	s.audit(r, nil, "alert_rule:delete", map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}
