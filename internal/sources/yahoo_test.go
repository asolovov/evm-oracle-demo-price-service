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

func newTestYahoo(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewYahoo(config.SourceConfig{Enabled: true, BaseURL: srv.URL, Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewYahoo: %v", err)
	}
	return a
}

func TestYahooHappyPath(t *testing.T) {
	a := newTestYahoo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ^GSPC must be percent-encoded in the path.
		if !strings.Contains(r.URL.Path, "/v8/finance/chart/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("User-Agent must be set")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chart":{"result":[{"meta":{"regularMarketPrice":7355.8,"regularMarketTime":1782835200,"currency":"USD"}}],"error":null}}`))
	}))

	got, err := a.Fetch(context.Background(), "^GSPC")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceYahoo {
		t.Fatalf("Source = %v, want Yahoo", got.Source)
	}
	if got.Price != 7355.8 {
		t.Fatalf("Price = %v, want 7355.8", got.Price)
	}
}

func TestYahooUnknownTicker404(t *testing.T) {
	a := newTestYahoo(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found"}}}`))
	}))
	_, err := a.Fetch(context.Background(), "NOPE=F")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestYahooChartError(t *testing.T) {
	a := newTestYahoo(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chart":{"result":null,"error":{"code":"Bad Request","description":"invalid"}}}`))
	}))
	_, err := a.Fetch(context.Background(), "GC=F")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for chart.error, got %v", err)
	}
}

func TestYahooUpstream500(t *testing.T) {
	a := newTestYahoo(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	_, err := a.Fetch(context.Background(), "GC=F")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
