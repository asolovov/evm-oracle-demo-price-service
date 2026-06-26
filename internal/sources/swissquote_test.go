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

func newTestSwissquote(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewSwissquote(config.SourceConfig{Enabled: true, BaseURL: srv.URL, Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewSwissquote: %v", err)
	}
	return a
}

func TestSwissquoteHappyPath(t *testing.T) {
	a := newTestSwissquote(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/instrument/XAU/USD") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"topo":{"platform":"MT5"},"spreadProfilePrices":[{"spreadProfile":"Standard","bid":4084.0,"ask":4085.12}]}]`))
	}))

	got, err := a.Fetch(context.Background(), "XAU")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceSwissquote {
		t.Fatalf("Source = %v, want Swissquote", got.Source)
	}
	// mid of 4084.0 / 4085.12
	if got.Price != 4084.56 {
		t.Fatalf("Price = %v, want 4084.56 (bid/ask mid)", got.Price)
	}
}

func TestSwissquoteNoUsableQuote(t *testing.T) {
	a := newTestSwissquote(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"spreadProfilePrices":[{"spreadProfile":"Standard","bid":0,"ask":0}]}]`))
	}))
	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestSwissquoteUpstream500(t *testing.T) {
	a := newTestSwissquote(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
