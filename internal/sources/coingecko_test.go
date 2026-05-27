package sources

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func newTestCoinGecko(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, err := NewCoinGecko(config.SourceConfig{
		Enabled:   true,
		BaseURL:   srv.URL,
		Timeout:   "2s",
		RateLimit: 0, // disable for tests
		Burst:     0,
	})
	if err != nil {
		t.Fatalf("NewCoinGecko: %v", err)
	}
	return a
}

func TestCoinGeckoHappyPath(t *testing.T) {
	a := newTestCoinGecko(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "ids=weth") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"weth":{"usd":3450.12,"last_updated_at":1716345600}}`))
	}))

	got, err := a.Fetch(context.Background(), "weth")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceCoinGecko {
		t.Fatalf("Source = %v, want CoinGecko", got.Source)
	}
	if got.Price != 3450.12 {
		t.Fatalf("Price = %v, want 3450.12", got.Price)
	}
	wantObserved := time.Unix(1716345600, 0).UTC()
	if !got.SourceObservedAt.Equal(wantObserved) {
		t.Fatalf("SourceObservedAt = %v, want %v", got.SourceObservedAt, wantObserved)
	}
}

func TestCoinGeckoMissingId(t *testing.T) {
	a := newTestCoinGecko(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// CoinGecko returns an empty object when the id is unknown.
		_, _ = w.Write([]byte(`{}`))
	}))

	_, err := a.Fetch(context.Background(), "weth")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestCoinGeckoUpstream500(t *testing.T) {
	a := newTestCoinGecko(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream broke", http.StatusInternalServerError)
	}))

	_, err := a.Fetch(context.Background(), "weth")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestCoinGeckoZeroPrice(t *testing.T) {
	a := newTestCoinGecko(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"weth":{"usd":0,"last_updated_at":1716345600}}`))
	}))

	_, err := a.Fetch(context.Background(), "weth")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for zero price, got %v", err)
	}
}
