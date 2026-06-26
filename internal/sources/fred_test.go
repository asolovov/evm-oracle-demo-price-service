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

func newTestFRED(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewFRED(config.SourceConfig{Enabled: true, BaseURL: srv.URL, APIKey: "test-key", Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewFRED: %v", err)
	}
	return a
}

func TestFREDHappyPath(t *testing.T) {
	a := newTestFRED(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "series_id=DCOILWTICO") {
			t.Errorf("expected series_id in query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"observations":[{"date":"2026-06-25","value":"69.21"},{"date":"2026-06-24","value":"70.10"}]}`))
	}))

	got, err := a.Fetch(context.Background(), "DCOILWTICO")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceFRED {
		t.Fatalf("Source = %v, want FRED", got.Source)
	}
	if got.Price != 69.21 {
		t.Fatalf("Price = %v, want 69.21", got.Price)
	}
}

// FRED returns "." for non-trading days; the adapter must skip them and use
// the most recent real observation.
func TestFREDSkipsBlankValues(t *testing.T) {
	a := newTestFRED(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"observations":[{"date":"2026-06-27","value":"."},{"date":"2026-06-26","value":"."},{"date":"2026-06-25","value":"6123.45"}]}`))
	}))

	got, err := a.Fetch(context.Background(), "SP500")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Price != 6123.45 {
		t.Fatalf("Price = %v, want 6123.45 (should skip the two '.' rows)", got.Price)
	}
	if got.SourceObservedAt.Format("2006-01-02") != "2026-06-25" {
		t.Fatalf("observed = %v, want the 2026-06-25 row", got.SourceObservedAt)
	}
}

func TestFREDAllBlank(t *testing.T) {
	a := newTestFRED(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"observations":[{"date":"2026-06-27","value":"."},{"date":"2026-06-26","value":"."}]}`))
	}))
	_, err := a.Fetch(context.Background(), "SP500")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData when all rows blank, got %v", err)
	}
}

func TestFREDRequiresAPIKey(t *testing.T) {
	_, err := NewFRED(config.SourceConfig{Enabled: true, BaseURL: "http://example.com", Timeout: "2s"})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for missing api key, got %v", err)
	}
}
