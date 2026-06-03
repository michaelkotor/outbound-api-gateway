// Command harness runs the layer-2 integration scenarios and layer-3 race cases
// against a live gateway + mock API stack and exits 0 (all passed/skipped) or 1
// (any failure).
//
// It is invoked via:
//
//	docker compose -f docker-compose.yml -f docker-compose.test.yml run --rm harness
//
// or locally with GATEWAY_URL / MOCK_URL pointing at running services.
//
// Scenarios share the contract func(t *testing.T, gw *client.GatewayClient,
// mock *client.MockClient). They are driven through the standard testing
// framework (testing.Main) so each prints a clear PASS / FAIL / SKIP line and a
// failing assertion aborts only its own scenario. The process exit code is the
// aggregate result.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/michaelkotor/outbound-api-gateway/test/harness/client"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/color"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/race"
	"github.com/michaelkotor/outbound-api-gateway/test/harness/scenarios"
)

const (
	gatewayHealthTimeout = 10 * time.Second
	mockHealthTimeout    = 5 * time.Second
)

// scenarioFn is the shared contract for layer-2 scenarios and layer-3 race cases.
type scenarioFn func(t *testing.T, gw *client.GatewayClient, mock *client.MockClient)

func main() {
	gw := client.NewGateway(envOr("GATEWAY_URL", "http://localhost:8080"))
	mock := client.NewMock(envOr("MOCK_URL", "http://localhost:9090"))

	log.Printf(color.Bold("harness")+": gateway=%s  mock=%s", color.Cyan(gw.BaseURL), color.Cyan(mock.BaseURL))

	// Mock must be up first so per-scenario /admin/reset works.
	log.Printf(color.Bold("harness")+": waiting for mockapi to become healthy (timeout=%s)", mockHealthTimeout)
	if err := mock.WaitHealthy(mockHealthTimeout); err != nil {
		log.Fatalf(color.Red("harness: %v"), err)
	}
	log.Print(color.Bold("harness") + ": mockapi is " + color.Green("healthy"))

	log.Printf(color.Bold("harness")+": waiting for gateway to become healthy (timeout=%s)", gatewayHealthTimeout)
	if err := gw.WaitHealthy(gatewayHealthTimeout); err != nil {
		log.Fatalf(color.Red("harness: %v"), err)
	}
	log.Print(color.Bold("harness") + ": gateway is " + color.Green("healthy"))

	// Layer 2 runs first, then layer 3, in registration order.
	registered := []struct {
		name string
		fn   scenarioFn
	}{
		// --- Layer 2: integration scenarios ---
		// Order matters: scenarios that count requests (round_robin, usage_api)
		// run while every key is healthy. The scenarios that cool keys
		// (cooldown_applied, pool_exhausted) run last, because the gateway has no
		// cooldown-reset hook and a 60s TTL, so a cooled key stays cooled for the
		// rest of the run.
		{"layer2/healthz", scenarios.Healthz},
		{"layer2/transparent_proxy", scenarios.TransparentProxy},
		{"layer2/unknown_route_returns_404", scenarios.UnknownRoute},
		{"layer2/key_injection", scenarios.KeyInjection},
		{"layer2/round_robin_distribution", scenarios.RoundRobinDistribution},
		{"layer2/usage_api_accuracy", scenarios.UsageAPI},
		{"layer2/limit_exhaustion_triggers_swap", scenarios.LimitExhaustion},
		{"layer2/upstream_timeout_propagated", scenarios.UpstreamTimeout},
		{"layer2/cooldown_recovery", scenarios.CooldownRecovery},
		{"layer2/cooldown_applied_on_429", scenarios.CooldownApplied},
		{"layer2/pool_exhausted_returns_503", scenarios.PoolExhausted},

		// --- Layer 3: race / load cases ---
		{"layer3/concurrent_requests_no_counter_drift", race.CounterDrift},
		{"layer3/concurrent_requests_no_key_reuse_after_exhaustion", race.NoOverrun},
		{"layer3/concurrent_feedback_no_double_cooldown", race.DoubleCooldown},
		{"layer3/window_reset_under_load", race.WindowReset},
	}

	log.Printf(color.Bold("harness")+": registered %s tests", color.Bold(fmt.Sprintf("%d", len(registered))))

	tests := make([]testing.InternalTest, 0, len(registered))
	for _, r := range registered {
		r := r
		tests = append(tests, testing.InternalTest{
			Name: r.name,
			F: func(t *testing.T) {
				// Each scenario starts from a clean mock state.
				if err := mock.Reset(); err != nil {
					t.Fatalf("pre-scenario mock reset: %v", err)
				}
				r.fn(t, gw, mock)
			},
		})
	}

	// Force verbose output so every scenario prints PASS / FAIL / SKIP.
	os.Args = append([]string{os.Args[0], "-test.v"}, os.Args[1:]...)

	// Pipe stdout through a line colorizer so the testing framework's own
	// === RUN / --- PASS / --- FAIL / --- SKIP lines are colored.
	startOutputColorizer()

	// testing.Main runs every test and calls os.Exit with the aggregate code:
	// 0 when all passed or skipped, 1 when any failed.
	testing.Main(
		func(pat, str string) (bool, error) { return regexp.MatchString(pat, str) },
		tests,
		nil,
		nil,
	)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// startOutputColorizer replaces os.Stdout with a pipe whose read-end is
// drained by a goroutine that colorizes the testing framework's own output
// lines (=== RUN, --- PASS, --- FAIL, --- SKIP, PASS, FAIL) before forwarding
// them to the original stdout.
func startOutputColorizer() {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return // fall back to plain output if the pipe cannot be created
	}
	os.Stdout = w

	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			fmt.Fprintln(origStdout, colorizeLine(scanner.Text()))
		}
	}()
}

// colorizeLine applies ANSI colors to lines emitted by the testing framework.
func colorizeLine(line string) string {
	switch {
	case strings.HasPrefix(line, "=== RUN"):
		return color.Blue(line)
	case strings.HasPrefix(line, "--- PASS"):
		return color.Green(line)
	case strings.HasPrefix(line, "--- FAIL"):
		return color.Red(line)
	case strings.HasPrefix(line, "--- SKIP"):
		return color.Yellow(line)
	case line == "PASS":
		return color.Green(line)
	case line == "FAIL":
		return color.Red(line)
	default:
		return line
	}
}
