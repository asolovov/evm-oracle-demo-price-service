package healthz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/module"
)

// fakeModule is a module.Module implementation that lets tests script the
// HealthCheck result, used to exercise the /readyz aggregation path.
type fakeModule struct {
	name string
	hc   error
}

func (m *fakeModule) Name() string                    { return m.name }
func (m *fakeModule) Init(context.Context) error      { return nil }
func (m *fakeModule) Start(context.Context) error     { return nil }
func (m *fakeModule) Stop(context.Context) error      { return nil }
func (m *fakeModule) HealthCheck(context.Context) error {
	return m.hc
}

// startTestModule binds the healthz module to an ephemeral port and starts
// it. Returns the base URL and a teardown.
func startTestModule(t *testing.T, manager *module.Manager) (string, func()) {
	t.Helper()

	// Grab an ephemeral port before binding so the test knows where to GET.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	mod := NewModule(&config.HealthzConfig{Host: "127.0.0.1", Port: addr.Port}, manager)
	if err := mod.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := mod.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(addr.Port)

	// Wait for the listener to come up.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := mod.HealthCheck(context.Background()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	teardown := func() {
		_ = mod.Stop(context.Background())
	}
	return base, teardown
}

func TestHealthzLiveness(t *testing.T) {
	mgr := module.NewManager()
	base, teardown := startTestModule(t, mgr)
	defer teardown()

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("body = %s, want status=ok", body)
	}
}

func TestReadyzAllHealthy(t *testing.T) {
	mgr := module.NewManager()
	mgr.Register(&fakeModule{name: "alpha", hc: nil})
	mgr.Register(&fakeModule{name: "beta", hc: nil})

	base, teardown := startTestModule(t, mgr)
	defer teardown()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestReadyzFailingPeerReturns503(t *testing.T) {
	mgr := module.NewManager()
	mgr.Register(&fakeModule{name: "alpha", hc: nil})
	mgr.Register(&fakeModule{name: "beta", hc: errors.New("db down")})

	base, teardown := startTestModule(t, mgr)
	defer teardown()

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "not_ready" {
		t.Fatalf("body status = %v, want not_ready", body["status"])
	}
	failing, _ := body["failing"].(map[string]any)
	if _, ok := failing["beta"]; !ok {
		t.Fatalf("expected 'beta' in failing map, got %+v", failing)
	}
}

func TestMetricsStub(t *testing.T) {
	mgr := module.NewManager()
	base, teardown := startTestModule(t, mgr)
	defer teardown()

	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "metrics not yet wired") {
		t.Fatalf("expected stub body, got %s", body)
	}
}

func TestHealthCheckBeforeStart(t *testing.T) {
	mgr := module.NewManager()
	mod := NewModule(&config.HealthzConfig{Host: "127.0.0.1", Port: 0}, mgr)
	// Init builds the http.Server but doesn't start listening.
	if err := mod.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := mod.HealthCheck(context.Background()); err == nil {
		t.Fatalf("HealthCheck should fail before Start, got nil")
	}
}
