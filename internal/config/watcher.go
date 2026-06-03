package config

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Watcher reloads configuration from a file on demand and on SIGHUP.
type Watcher struct {
	path string
}

// NewWatcher constructs a config watcher bound to a config file path.
func NewWatcher(path string) *Watcher {
	return &Watcher{path: path}
}

// Reload re-reads and parses the configuration file.
func (watcher *Watcher) Reload() (*Config, error) {
	return Load(watcher.path)
}

// Watch blocks until ctx is cancelled, reloading the configuration on every
// SIGHUP. A successful reload is delivered to onReload; a failed reload is
// delivered to onError and the previously loaded configuration stays in effect.
func (watcher *Watcher) Watch(ctx context.Context, onReload func(*Config), onError func(error)) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	defer signal.Stop(signals)

	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			reloadedConfig, err := watcher.Reload()
			if err != nil {
				if onError != nil {
					onError(err)
				}
				continue
			}
			if onReload != nil {
				onReload(reloadedConfig)
			}
		}
	}
}
