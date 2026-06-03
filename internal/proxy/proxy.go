// Package proxy provides the key-injecting reverse proxy for a route.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/metrics"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// ctxKey is the unexported type for the per-request selected key.
type ctxKey int

const keyContextKey ctxKey = iota

// New builds a reverse-proxy handler for a single route. For each request it
// asks the selector for an eligible key, injects the configured headers (with
// "{key}" replaced by the secret), forwards to the upstream, and records the
// result via a tracked transport. It returns 503 when the key pool is
// exhausted.
func New(route config.Route, keySelector selector.KeySelector, usageStorage storage.Storage) (http.Handler, error) {
	target, err := url.Parse(route.Upstream)
	if err != nil {
		return nil, fmt.Errorf("proxy: route %q: invalid upstream %q: %w", route.Name, route.Upstream, err)
	}
	if target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("proxy: route %q: upstream %q must be an absolute URL", route.Name, route.Upstream)
	}

	prefix := strings.TrimRight(route.Prefix, "/")

	windowsByKey := make(map[string][]time.Duration, len(route.Keys))
	for _, keyConfig := range route.Keys {
		windowDurations := make([]time.Duration, 0, len(keyConfig.Limits))
		for _, limit := range keyConfig.Limits {
			windowDurations = append(windowDurations, limit.Window)
		}
		windowsByKey[keyConfig.Name] = windowDurations
	}

	reverseProxy := &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			request.Host = target.Host
			// Strip the route prefix; the remainder of the path and the raw
			// query string are forwarded to the upstream unchanged.
			request.URL.Path = stripPrefix(request.URL.Path, prefix)
			setForwardedFor(request)

			selectedKey, _ := request.Context().Value(keyContextKey).(keys.Key)
			for _, headerName := range route.Headers.Strip {
				request.Header.Del(headerName)
			}
			for headerName, headerTemplate := range route.Headers.Inject {
				request.Header.Set(headerName, strings.ReplaceAll(headerTemplate, "{key}", selectedKey.Value))
			}
		},
		Transport: &trackedTransport{
			baseTransport: http.DefaultTransport,
			selector:      keySelector,
			usageStorage:  usageStorage,
			route:         route.Name,
			windowsByKey:  windowsByKey,
		},
		ErrorHandler: func(responseWriter http.ResponseWriter, request *http.Request, err error) {
			log.Printf("proxy route=%s upstream_error=%v", route.Name, err)
			http.Error(responseWriter, "upstream request failed", http.StatusBadGateway)
		},
	}

	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		selectedKey, err := keySelector.Next(request.Context(), route.Name)
		if errors.Is(err, selector.ErrPoolExhausted) {
			metrics.RecordPoolExhausted(route.Name)
			http.Error(responseWriter, "no keys available", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(responseWriter, "key selection failed", http.StatusInternalServerError)
			return
		}
		metrics.RecordSelection(route.Name, selectedKey.Name)
		requestContext := context.WithValue(request.Context(), keyContextKey, selectedKey)
		if route.UpstreamTimeout > 0 {
			var cancelUpstream context.CancelFunc
			requestContext, cancelUpstream = context.WithTimeout(requestContext, route.UpstreamTimeout)
			defer cancelUpstream()
		}
		reverseProxy.ServeHTTP(responseWriter, request.WithContext(requestContext))
	})

	return logging(route, target, handler), nil
}

// stripPrefix removes the route prefix from path, always returning a path with
// a leading slash so the upstream receives a well-formed request line.
func stripPrefix(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	if trimmed == "" || trimmed[0] != '/' {
		trimmed = "/" + trimmed
	}
	return trimmed
}

// setForwardedFor appends the client IP to the X-Forwarded-For chain.
func setForwardedFor(request *http.Request) {
	clientIP, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return
	}
	if priorForwardedFor := request.Header.Get("X-Forwarded-For"); priorForwardedFor != "" {
		clientIP = priorForwardedFor + ", " + clientIP
	}
	request.Header.Set("X-Forwarded-For", clientIP)
}

// logging wraps the proxy handler to log method, path, upstream, status, and
// latency for each request.
func logging(route config.Route, target *url.URL, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		startTime := time.Now()
		statusRecorder := &statusRecordingWriter{ResponseWriter: responseWriter, status: http.StatusOK}
		next.ServeHTTP(statusRecorder, request)
		latency := time.Since(startTime)
		metrics.RecordRequest(route.Name, request.Method, statusRecorder.status, latency)
		log.Printf("proxy route=%s method=%s path=%s upstream=%s status=%d latency=%s",
			route.Name, request.Method, request.URL.Path, target.String(), statusRecorder.status, latency)
	})
}

// statusRecordingWriter captures the response status code for logging.
type statusRecordingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (statusRecorder *statusRecordingWriter) WriteHeader(code int) {
	if !statusRecorder.wroteHeader {
		statusRecorder.status = code
		statusRecorder.wroteHeader = true
	}
	statusRecorder.ResponseWriter.WriteHeader(code)
}

func (statusRecorder *statusRecordingWriter) Write(payload []byte) (int, error) {
	if !statusRecorder.wroteHeader {
		statusRecorder.wroteHeader = true
	}
	return statusRecorder.ResponseWriter.Write(payload)
}
