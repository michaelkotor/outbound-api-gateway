// Command gateway is the outbound API gateway HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/michaelkotor/outbound-api-gateway/internal/api"
	"github.com/michaelkotor/outbound-api-gateway/internal/config"
	"github.com/michaelkotor/outbound-api-gateway/internal/keys"
	"github.com/michaelkotor/outbound-api-gateway/internal/metrics"
	"github.com/michaelkotor/outbound-api-gateway/internal/proxy"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/leastused"
	"github.com/michaelkotor/outbound-api-gateway/internal/selector/roundrobin"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/memory"
	"github.com/michaelkotor/outbound-api-gateway/internal/storage/redis"
)

const (
	shutdownTimeout = 5 * time.Second
	cooldownTTL     = 60 * time.Second
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	envFile := flag.String("env-file", ".env", "path to a .env file with key secrets (optional)")
	flag.Parse()

	// Load secrets from a .env file so running the binary directly behaves like
	// docker-compose's env_file. Existing process environment variables always
	// take precedence (godotenv does not overwrite them), and a missing file is
	// not an error — the secrets may already be exported.
	if err := godotenv.Load(*envFile); err != nil {
		log.Printf("gateway: no env file loaded from %q (%v); using the process environment", *envFile, err)
	} else {
		log.Printf("gateway: loaded environment from %s", *envFile)
	}

	if err := run(*configPath); err != nil {
		log.Fatalf("gateway: %v", err)
	}
}

func run(configPath string) error {
	loadedConfig, err := config.Load(configPath)
	if err != nil {
		return err
	}

	usageStorage, err := newStorage(loadedConfig)
	if err != nil {
		return err
	}

	initialHandler, err := buildHandler(loadedConfig, usageStorage)
	if err != nil {
		return err
	}

	liveHandler := &swappableHandler{}
	liveHandler.set(initialHandler)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Hot-reload routes/keys/selectors on SIGHUP. The storage backend is reused
	// so usage counters survive a reload; storage adapter/address and the listen
	// address only take effect on a full restart.
	watcher := config.NewWatcher(configPath)
	go watcher.Watch(ctx, func(reloadedConfig *config.Config) {
		rebuiltHandler, buildErr := buildHandler(reloadedConfig, usageStorage)
		if buildErr != nil {
			log.Printf("gateway: config reload rejected, keeping previous config: %v", buildErr)
			return
		}
		liveHandler.set(rebuiltHandler)
		log.Println("gateway: configuration reloaded (storage adapter/address and listen address require a restart)")
	}, func(reloadErr error) {
		log.Printf("gateway: config reload error: %v", reloadErr)
	})

	server := &http.Server{
		Addr:        loadedConfig.Server.Address,
		Handler:     liveHandler,
		ReadTimeout: loadedConfig.Server.ReadTimeout,
	}

	listenErrors := make(chan error, 1)
	go func() {
		log.Printf("gateway: listening on %s", loadedConfig.Server.Address)
		if listenErr := server.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			listenErrors <- listenErr
		}
	}()

	select {
	case listenErr := <-listenErrors:
		return listenErr
	case <-ctx.Done():
		log.Println("gateway: shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}
	log.Println("gateway: stopped cleanly")
	return nil
}

// buildHandler constructs the full HTTP router for a config against an existing
// storage instance: health, Prometheus metrics, the per-route proxies, and the
// usage API. It re-resolves keys and rebuilds selectors, so it is safe to call
// again on a config reload.
func buildHandler(loadedConfig *config.Config, usageStorage storage.Storage) (http.Handler, error) {
	router := chi.NewRouter()
	router.Use(middleware.Recoverer)

	router.Get("/healthz", func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusOK)
		_, _ = responseWriter.Write([]byte(`{"status":"ok"}`))
	})
	router.Handle("/metrics", metrics.Handler())

	fingerprints := make(map[string]string)
	configuredLimits := make(map[string]map[time.Duration]int64)
	for _, route := range loadedConfig.Routes {
		resolvedKeys, err := resolveKeys(route)
		if err != nil {
			return nil, err
		}
		for _, resolvedKey := range resolvedKeys {
			fingerprints[resolvedKey.Name] = resolvedKey.Fingerprint
			windowLimits := make(map[time.Duration]int64, len(resolvedKey.Limits))
			for _, limit := range resolvedKey.Limits {
				windowLimits[limit.Window] = limit.MaxRequests
			}
			configuredLimits[resolvedKey.Name] = windowLimits
		}

		routeCooldownTTL := route.CooldownTTL
		if routeCooldownTTL == 0 {
			routeCooldownTTL = cooldownTTL
		}
		keySelector, err := newKeySelector(route.Selector, usageStorage, resolvedKeys, routeCooldownTTL)
		if err != nil {
			return nil, fmt.Errorf("gateway: route %q: %w", route.Name, err)
		}

		handler, err := proxy.New(route, keySelector, usageStorage)
		if err != nil {
			return nil, err
		}

		prefix := strings.TrimRight(route.Prefix, "/")
		if prefix == "" {
			return nil, fmt.Errorf("gateway: route %q has an empty prefix", route.Name)
		}
		router.Handle(prefix, handler)
		router.Handle(prefix+"/*", handler)
		log.Printf("gateway: mounted route %s on %s -> %s (selector=%s, keys=%d)",
			route.Name, prefix, route.Upstream, route.Selector, len(resolvedKeys))
	}

	usageHandler := api.NewHandler(usageStorage, fingerprints, configuredLimits)
	router.Get("/usage", usageHandler.Usage)
	router.Get("/usage/{key_name}", usageHandler.UsageByKey)

	return router, nil
}

// swappableHandler is an http.Handler whose delegate can be replaced atomically
// while requests are in flight, used to apply config reloads without downtime.
type swappableHandler struct {
	current atomic.Pointer[http.Handler]
}

func (swappable *swappableHandler) set(handler http.Handler) {
	swappable.current.Store(&handler)
}

func (swappable *swappableHandler) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	(*swappable.current.Load()).ServeHTTP(responseWriter, request)
}

// newStorage builds the usage store backend selected in the config.
func newStorage(loadedConfig *config.Config) (storage.Storage, error) {
	switch loadedConfig.Storage.Adapter {
	case "", "memory":
		return memory.New(), nil
	case "redis":
		return redis.New(loadedConfig.Storage.RedisURL)
	default:
		return nil, fmt.Errorf("gateway: unknown storage adapter %q", loadedConfig.Storage.Adapter)
	}
}

// resolveKeys resolves every configured key for a route, failing fast if any
// env var is unset or empty.
func resolveKeys(route config.Route) ([]keys.Key, error) {
	resolvedKeys := make([]keys.Key, 0, len(route.Keys))
	for _, keyConfig := range route.Keys {
		resolvedKey, err := keys.Resolve(keyConfig)
		if err != nil {
			return nil, fmt.Errorf("gateway: route %q: %w", route.Name, err)
		}
		resolvedKeys = append(resolvedKeys, resolvedKey)
	}
	return resolvedKeys, nil
}

// newKeySelector constructs the key selector named in the route config.
func newKeySelector(selectorName string, usageStorage storage.Storage, resolvedKeys []keys.Key, cooldownDuration time.Duration) (selector.KeySelector, error) {
	switch selectorName {
	case "", "round_robin":
		return roundrobin.New(usageStorage, resolvedKeys, cooldownDuration), nil
	case "least_used":
		return leastused.New(usageStorage, resolvedKeys, cooldownDuration), nil
	default:
		return nil, fmt.Errorf("unsupported selector %q", selectorName)
	}
}
