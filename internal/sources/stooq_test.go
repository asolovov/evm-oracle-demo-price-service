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

func newTestStooq(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewStooq(config.SourceConfig{Enabled: true, BaseURL: srv.URL, Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewStooq: %v", err)
	}
	return a
}

const stooqHappyCSV = `Symbol,Date,Time,Open,High,Low,Close,Volume
XAUUSD,2026-05-21,18:45:00,2410.5,2415.0,2406.2,2412.8,0
`

func TestStooqHappyPath(t *testing.T) {
	a := newTestStooq(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "s=xauusd") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(stooqHappyCSV))
	}))

	got, err := a.Fetch(context.Background(), "xauusd")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceStooq {
		t.Fatalf("Source = %v, want Stooq", got.Source)
	}
	if got.Price != 2412.8 {
		t.Fatalf("Price = %v, want 2412.8", got.Price)
	}
	if got.SourceObservedAt.IsZero() {
		t.Fatalf("SourceObservedAt should be parsed from Date+Time")
	}
}

func TestStooqLowercasesSymbol(t *testing.T) {
	a := newTestStooq(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "s=xauusd") {
			t.Errorf("symbol not lowercased: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(stooqHappyCSV))
	}))

	if _, err := a.Fetch(context.Background(), "XAUUSD"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
}

func TestStooqNoDataRow(t *testing.T) {
	a := newTestStooq(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("Symbol,Date,Time,Open,High,Low,Close,Volume\n"))
	}))

	_, err := a.Fetch(context.Background(), "xauusd")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestStooqNotDeliveredClose(t *testing.T) {
	a := newTestStooq(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(`Symbol,Date,Time,Open,High,Low,Close,Volume
XAUUSD,2026-05-21,18:45:00,N/D,N/D,N/D,N/D,N/D
`))
	}))

	_, err := a.Fetch(context.Background(), "xauusd")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for N/D close, got %v", err)
	}
}

func TestStooqUpstream500(t *testing.T) {
	a := newTestStooq(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))

	_, err := a.Fetch(context.Background(), "xauusd")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
