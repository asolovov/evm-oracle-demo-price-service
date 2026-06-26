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

func newTestGoldAPI(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewGoldAPI(config.SourceConfig{Enabled: true, BaseURL: srv.URL, Timeout: "2s"})
	if err != nil {
		t.Fatalf("NewGoldAPI: %v", err)
	}
	return a
}

func TestGoldAPIHappyPath(t *testing.T) {
	a := newTestGoldAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/price/XAU") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Gold","price":4082.5,"symbol":"XAU","updatedAt":"2026-06-26T17:00:00Z"}`))
	}))

	got, err := a.Fetch(context.Background(), "XAU")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceGoldAPI {
		t.Fatalf("Source = %v, want GoldAPI", got.Source)
	}
	if got.Price != 4082.5 {
		t.Fatalf("Price = %v, want 4082.5", got.Price)
	}
	if got.SourceObservedAt.IsZero() {
		t.Fatalf("SourceObservedAt should parse from updatedAt")
	}
}

func TestGoldAPINotFound(t *testing.T) {
	a := newTestGoldAPI(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	}))
	_, err := a.Fetch(context.Background(), "ZZZ")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}

func TestGoldAPIZeroPrice(t *testing.T) {
	a := newTestGoldAPI(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"price":0,"symbol":"XAU"}`))
	}))
	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for zero price, got %v", err)
	}
}

func TestGoldAPIUpstream500(t *testing.T) {
	a := newTestGoldAPI(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream, got %v", err)
	}
}
