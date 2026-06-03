// Command mockapi is a standalone, in-memory echo/rate-limit API used only for
// integration and race testing of the gateway. It shares no code with the
// gateway. See test/MOCK_API.md for the full spec.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 5 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := loadConfig()
	if err != nil {
		logger.Error("invalid configuration", "error", err.Error())
		os.Exit(1)
	}

	registry, err := parseRegistry(cfg.RawKeys)
	if err != nil {
		logger.Error("invalid MOCK_KEYS", "error", err.Error())
		os.Exit(1)
	}

	limiter := newLimiter(registry, cfg.DefaultLimit, cfg.Window)
	srv := newServer(registry, limiter, cfg.LatencyMS)

	// Log each registered key by name and fingerprint. Never log token values.
	for _, name := range registry.names {
		rec := registry.byName[name]
		logger.Info("registered key", "name", rec.Name, "fingerprint", rec.Fingerprint)
	}
	logger.Info("mockapi starting",
		"port", cfg.Port,
		"default_limit", cfg.DefaultLimit,
		"window", cfg.Window.String(),
		"latency_ms", cfg.LatencyMS,
		"keys", len(registry.names),
	)

	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: srv.routes(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("server error", "error", err.Error())
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("mockapi stopped cleanly")
}
