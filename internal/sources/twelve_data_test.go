package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func newTestTwelveData(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewTwelveData(config.SourceConfig{
		Enabled: true,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Timeout: "2s",
	})
	if err != nil {
		t.Fatalf("NewTwelveData: %v", err)
	}
	return a
}

func TestTwelveDataHappyPath(t *testing.T) {
	a := newTestTwelveData(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "symbol=XAU%2FUSD") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "apikey=test-key") {
			t.Errorf("api key missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"price":"2412.85"}`))
	}))

	got, err := a.Fetch(context.Background(), "XAU/USD")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceTwelveData {
		t.Fatalf("Source = %v, want TwelveData", got.Source)
	}
	if got.Price != 2412.85 {
		t.Fatalf("Price = %v, want 2412.85", got.Price)
	}
}

func TestTwelveDataBadSymbolNoData(t *testing.T) {
	a := newTestTwelveData(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 200 OK + status=error + 4xx code = client-side / unknown symbol.
		_, _ = w.Write([]byte(`{"code":400,"message":"symbol not found","status":"error"}`))
	}))

	_, err := a.Fetch(context.Background(), "FAKE")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestTwelveDataUpstreamErrorStatus(t *testing.T) {
	a := newTestTwelveData(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// status=error + 5xx code = server-side.
		_, _ = w.Write([]byte(`{"code":500,"message":"upstream","status":"error"}`))
	}))

	_, err := a.Fetch(context.Background(), "XAU/USD")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestTwelveDataRequiresAPIKey(t *testing.T) {
	_, err := NewTwelveData(config.SourceConfig{Enabled: true, BaseURL: "http://example.com", Timeout: "2s"})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for missing api key, got %v", err)
	}
}

func TestTwelveDataEmptyPrice(t *testing.T) {
	a := newTestTwelveData(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"price":""}`))
	}))

	_, err := a.Fetch(context.Background(), "XAU/USD")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for empty price, got %v", err)
	}
}
