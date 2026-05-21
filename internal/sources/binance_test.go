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

func newTestBinance(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, err := NewBinance(config.SourceConfig{
		Enabled: true,
		BaseURL: srv.URL,
		Timeout: "2s",
	})
	if err != nil {
		t.Fatalf("NewBinance: %v", err)
	}
	return a
}

func TestBinanceHappyPath(t *testing.T) {
	a := newTestBinance(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "symbol=ETHUSDT") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"ETHUSDT","price":"3450.12000000"}`))
	}))

	got, err := a.Fetch(context.Background(), "ETHUSDT")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceBinance {
		t.Fatalf("Source = %v, want Binance", got.Source)
	}
	if got.Price != 3450.12 {
		t.Fatalf("Price = %v, want 3450.12", got.Price)
	}
}

func TestBinanceUnknownSymbol(t *testing.T) {
	a := newTestBinance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	}))

	_, err := a.Fetch(context.Background(), "FOOUSDT")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestBinanceUpstream500(t *testing.T) {
	a := newTestBinance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server fire", http.StatusInternalServerError)
	}))

	_, err := a.Fetch(context.Background(), "ETHUSDT")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestBinanceZeroPrice(t *testing.T) {
	a := newTestBinance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"ETHUSDT","price":"0"}`))
	}))

	_, err := a.Fetch(context.Background(), "ETHUSDT")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for zero price, got %v", err)
	}
}

func TestBinanceUnparseablePrice(t *testing.T) {
	a := newTestBinance(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"ETHUSDT","price":"not-a-number"}`))
	}))

	_, err := a.Fetch(context.Background(), "ETHUSDT")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream for malformed price, got %v", err)
	}
}

func TestBinanceRejectsBadTimeout(t *testing.T) {
	_, err := NewBinance(config.SourceConfig{
		Enabled: true,
		BaseURL: "http://example.com",
		Timeout: "definitely-not-a-duration",
	})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig, got %v", err)
	}
}
