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

func newTestEIA(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewEIA(config.SourceConfig{Enabled: true, BaseURL: srv.URL, APIKey: "test-key", Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewEIA: %v", err)
	}
	return a
}

func TestEIAHappyPath(t *testing.T) {
	a := newTestEIA(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "RWTC") {
			t.Errorf("expected RWTC series in query: %s", r.URL.RawQuery)
		}
		if !strings.Contains(r.URL.RawQuery, "api_key=test-key") {
			t.Errorf("api key missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"data":[{"period":"2026-06-22","value":78.94}]}}`))
	}))

	got, err := a.Fetch(context.Background(), "RWTC")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceEIA {
		t.Fatalf("Source = %v, want EIA", got.Source)
	}
	if got.Price != 78.94 {
		t.Fatalf("Price = %v, want 78.94", got.Price)
	}
}

func TestEIANoRows(t *testing.T) {
	a := newTestEIA(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"data":[]}}`))
	}))
	_, err := a.Fetch(context.Background(), "RWTC")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestEIAUpstream500(t *testing.T) {
	a := newTestEIA(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	_, err := a.Fetch(context.Background(), "RWTC")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}

func TestEIARequiresAPIKey(t *testing.T) {
	_, err := NewEIA(config.SourceConfig{Enabled: true, BaseURL: "http://example.com", Timeout: "2s"})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for missing api key, got %v", err)
	}
}
