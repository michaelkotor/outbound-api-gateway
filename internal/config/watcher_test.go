package config_test

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/michaelkotor/outbound-api-gateway/internal/config"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "gateway-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}

const minimalConfig = `
server:
  addr: ":9090"
routes:
  - name: test-api
    prefix: /api
    upstream: https://api.example.com
    keys:
      - name: key1
        env: TEST_WATCHER_KEY
`

func TestNewWatcher_ReloadReadsFile(t *testing.T) {
	path := writeTempConfig(t, minimalConfig)
	watcher := config.NewWatcher(path)

	cfg, err := watcher.Reload()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, ":9090", cfg.Server.Address)
}

func TestNewWatcher_ReloadErrorOnMissingFile(t *testing.T) {
	watcher := config.NewWatcher("/nonexistent/path/config.yaml")
	_, err := watcher.Reload()
	require.Error(t, err)
}

func TestNewWatcher_ReloadErrorOnInvalidYAML(t *testing.T) {
	path := writeTempConfig(t, "[[[invalid yaml")
	watcher := config.NewWatcher(path)

	_, err := watcher.Reload()
	require.Error(t, err)
}

func TestNewWatcher_ReloadErrorOnInvalidUpstream(t *testing.T) {
	badConfig := `
server:
  addr: ":9090"
routes:
  - name: bad-route
    prefix: /api
    upstream: not-a-valid-url
`
	path := writeTempConfig(t, badConfig)
	watcher := config.NewWatcher(path)

	_, err := watcher.Reload()
	require.Error(t, err)
}

func TestWatch_ExitsOnContextCancel(t *testing.T) {
	path := writeTempConfig(t, minimalConfig)
	watcher := config.NewWatcher(path)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watcher.Watch(ctx, nil, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after context cancel")
	}
}

func TestWatch_SIGHUPTriggersOnReload(t *testing.T) {
	path := writeTempConfig(t, minimalConfig)
	watcher := config.NewWatcher(path)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloaded := make(chan *config.Config, 1)
	watchReady := make(chan struct{})

	go func() {
		close(watchReady)
		watcher.Watch(ctx, func(cfg *config.Config) {
			reloaded <- cfg
		}, nil)
	}()

	// Wait until Watch goroutine has started, then give it additional time to
	// register the signal handler.
	<-watchReady
	time.Sleep(100 * time.Millisecond)

	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGHUP))

	select {
	case cfg := <-reloaded:
		require.NotNil(t, cfg)
		assert.Equal(t, ":9090", cfg.Server.Address)
	case <-time.After(3 * time.Second):
		t.Fatal("onReload was not called after SIGHUP")
	}
}
