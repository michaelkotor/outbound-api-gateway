package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// handleAdminState returns a snapshot of every key's current state.
func (s *Server) handleAdminState(w http.ResponseWriter, _ *http.Request) {
	snaps := s.limiter.Snapshot()
	keys := make([]map[string]any, 0, len(snaps))
	var window string
	for _, st := range snaps {
		window = st.Window.String()
		keys = append(keys, map[string]any{
			"name":         st.Name,
			"fingerprint":  st.Fingerprint,
			"request_n":    st.RequestN,
			"window_count": st.WindowCount,
			"limit":        st.Limit,
			"cooled_until": rfc3339OrNil(st.CooledUntil),
			"resets_at":    st.ResetsAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot_at": time.Now().Format(time.RFC3339),
		"window":      window,
		"keys":        keys,
	})
}

// handleAdminReset zeroes window counters and clears cooldowns for all keys.
func (s *Server) handleAdminReset(w http.ResponseWriter, _ *http.Request) {
	n := s.limiter.Reset()
	writeJSON(w, http.StatusOK, map[string]any{
		"reset":      true,
		"keys_reset": n,
	})
}

// setLimitRequest is the body of POST /admin/set-limit.
type setLimitRequest struct {
	KeyName string  `json:"key_name"`
	Limit   *int64  `json:"limit"`
	Window  *string `json:"window"`
}

// handleAdminSetLimit overrides a key's limit (and optionally its window).
func (s *Server) handleAdminSetLimit(w http.ResponseWriter, r *http.Request) {
	var req setLimitRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body", "detail": err.Error()})
		return
	}
	if req.KeyName == "" || req.Limit == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body", "detail": "key_name and limit are required"})
		return
	}

	var window *time.Duration
	if req.Window != nil {
		d, err := time.ParseDuration(*req.Window)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_window", "detail": err.Error()})
			return
		}
		window = &d
	}

	if err := s.limiter.SetLimit(req.KeyName, *req.Limit, window); err != nil {
		if errors.Is(err, errKeyNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "key_name": req.KeyName})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"updated":  true,
		"key_name": req.KeyName,
		"limit":    *req.Limit,
	})
}

// setLatencyRequest is the body of POST /admin/set-latency.
type setLatencyRequest struct {
	LatencyMS *int64 `json:"latency_ms"`
}

// handleAdminSetLatency updates the artificial latency applied to echo responses.
func (s *Server) handleAdminSetLatency(w http.ResponseWriter, r *http.Request) {
	var req setLatencyRequest
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body", "detail": err.Error()})
		return
	}
	if req.LatencyMS == nil || *req.LatencyMS < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body", "detail": "latency_ms must be a non-negative integer"})
		return
	}
	s.latencyMS.Store(*req.LatencyMS)
	writeJSON(w, http.StatusOK, map[string]any{"latency_ms": *req.LatencyMS})
}

// decodeJSON decodes a JSON body, tolerating an empty body as an empty object.
func decodeJSON(body io.Reader, dst any) error {
	dec := json.NewDecoder(body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty body == {}
		}
		return err
	}
	return nil
}

func rfc3339OrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}
