package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

// Server wires the mock API HTTP handlers over the registry and limiter.
type Server struct {
	registry  *Registry
	limiter   *Limiter
	latencyMS atomic.Int64 // mutable via /admin/set-latency, read on every request
}

func newServer(reg *Registry, lim *Limiter, initialLatencyMS int64) *Server {
	s := &Server{registry: reg, limiter: lim}
	s.latencyMS.Store(initialLatencyMS)
	return s
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// The three echo paths behave identically.
	r.Post("/v1/chat", s.handleEcho)
	r.Get("/v1/models", s.handleEcho)
	r.Get("/v1/status", s.handleEcho)

	r.Get("/admin/state", s.handleAdminState)
	r.Post("/admin/reset", s.handleAdminReset)
	r.Post("/admin/set-limit", s.handleAdminSetLimit)
	r.Post("/admin/set-latency", s.handleAdminSetLatency)

	return r
}

// handleEcho authenticates the request, applies artificial latency, enforces
// the rate limit, and echoes the serving key on success.
func (s *Server) handleEcho(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r.Header.Get("Authorization"))
	rec := s.registry.lookupByToken(token)
	if rec == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error":  "unauthorized",
			"detail": "unknown or missing token",
		})
		return
	}

	if ms := s.latencyMS.Load(); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}

	d := s.limiter.Allow(rec.Name)
	if !d.Allowed {
		retryAfter := int64(time.Until(d.ResetsAt).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":     "rate_limit_exceeded",
			"key_name":  d.KeyName,
			"limit":     d.Limit,
			"window":    d.Window.String(),
			"resets_at": d.ResetsAt.Format(time.RFC3339),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"key_name":     d.KeyName,
		"request_n":    d.RequestN,
		"window_count": d.WindowCount,
		"limit":        d.Limit,
		"window":       d.Window.String(),
		"resets_at":    d.ResetsAt.Format(time.RFC3339),
	})
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
