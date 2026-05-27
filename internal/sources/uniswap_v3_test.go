package sources

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func newTestUniswap(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewUniswapV3(config.SourceConfig{
		Enabled: true,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Timeout: "2s",
	})
	if err != nil {
		t.Fatalf("NewUniswapV3: %v", err)
	}
	return a
}

// readBody pulls the GraphQL request body out for assertions.
func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// token0=USDC (stable), token1=WETH — token0Price ("USDC per WETH") is the
// USD price of WETH.
func TestUniswapV3Token0Stable(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/test-key/subgraphs/id/") {
			t.Errorf("expected gateway path with api key, got %s", r.URL.Path)
		}
		if !strings.Contains(readBody(t, r), "0xpool") {
			t.Errorf("pool address missing from body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pool":{
			"token0":{"symbol":"USDC"},
			"token1":{"symbol":"WETH"},
			"token0Price":"3450.50",
			"token1Price":"0.00029"
		}}}`))
	}))

	got, err := a.Fetch(context.Background(), "0xpool")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceUniswapV3 {
		t.Fatalf("Source = %v", got.Source)
	}
	if got.Price != 3450.50 {
		t.Fatalf("Price = %v, want 3450.50", got.Price)
	}
}

// token0=WBTC, token1=USDC — token1Price ("USDC per WBTC") is the USD price.
func TestUniswapV3Token1Stable(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pool":{
			"token0":{"symbol":"WBTC"},
			"token1":{"symbol":"USDC"},
			"token0Price":"0.0000166",
			"token1Price":"60000"
		}}}`))
	}))

	got, err := a.Fetch(context.Background(), "0xpool")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Price != 60000 {
		t.Fatalf("Price = %v, want 60000", got.Price)
	}
}

func TestUniswapV3NoStableSide(t *testing.T) {
	// WETH/WBTC pool — no stable side, should be ErrNoData.
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pool":{
			"token0":{"symbol":"WBTC"},
			"token1":{"symbol":"WETH"},
			"token0Price":"17",
			"token1Price":"0.058"
		}}}`))
	}))

	_, err := a.Fetch(context.Background(), "0xpool")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for non-stable pool, got %v", err)
	}
}

func TestUniswapV3StableStable(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pool":{
			"token0":{"symbol":"USDC"},
			"token1":{"symbol":"DAI"},
			"token0Price":"1.0001",
			"token1Price":"0.9999"
		}}}`))
	}))

	_, err := a.Fetch(context.Background(), "0xpool")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for stable/stable pool, got %v", err)
	}
}

func TestUniswapV3PoolNotFound(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pool":null}}`))
	}))

	_, err := a.Fetch(context.Background(), "0xpool")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for null pool, got %v", err)
	}
}

func TestUniswapV3GraphErrors(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"indexer is not synced"}]}`))
	}))

	_, err := a.Fetch(context.Background(), "0xpool")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream for graph errors, got %v", err)
	}
}

func TestUniswapV3RequiresAPIKey(t *testing.T) {
	_, err := NewUniswapV3(config.SourceConfig{Enabled: true, BaseURL: "http://example.com", Timeout: "2s"})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for missing api key, got %v", err)
	}
}

func TestUniswapV3Upstream500(t *testing.T) {
	a := newTestUniswap(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))

	_, err := a.Fetch(context.Background(), "0xpool")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
