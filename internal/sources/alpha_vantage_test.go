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

func newTestAlphaVantage(t *testing.T, handler http.Handler) Adapter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a, err := NewAlphaVantage(config.SourceConfig{
		Enabled: true,
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Timeout: "2s",
	})
	if err != nil {
		t.Fatalf("NewAlphaVantage: %v", err)
	}
	return a
}

// XAU / XAG go through CURRENCY_EXCHANGE_RATE.
func TestAlphaVantageFXHappyPath(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "function=CURRENCY_EXCHANGE_RATE") {
			t.Errorf("expected CURRENCY_EXCHANGE_RATE, got %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Realtime Currency Exchange Rate":{
			"1. From_Currency Code":"XAU",
			"3. To_Currency Code":"USD",
			"5. Exchange Rate":"2412.8500",
			"6. Last Refreshed":"2026-05-21 18:30:00",
			"7. Time Zone":"UTC"
		}}`))
	}))

	got, err := a.Fetch(context.Background(), "XAU")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Source != models.SourceAlphaVantage {
		t.Fatalf("Source = %v", got.Source)
	}
	if got.Price != 2412.85 {
		t.Fatalf("Price = %v, want 2412.85", got.Price)
	}
	if got.SourceObservedAt.IsZero() {
		t.Fatalf("SourceObservedAt should be parsed from Last Refreshed")
	}
}

// SPX / WTI / HG go through GLOBAL_QUOTE.
func TestAlphaVantageQuoteHappyPath(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "function=GLOBAL_QUOTE") {
			t.Errorf("expected GLOBAL_QUOTE, got %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Global Quote":{
			"01. symbol":"SPX",
			"05. price":"5230.4500",
			"07. latest trading day":"2026-05-21"
		}}`))
	}))

	got, err := a.Fetch(context.Background(), "SPX")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Price != 5230.45 {
		t.Fatalf("Price = %v, want 5230.45", got.Price)
	}
}

func TestAlphaVantageRateLimitNote(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Note":"Thank you for using Alpha Vantage! Our standard API call frequency is 5 calls per minute and 500 calls per day."}`))
	}))

	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream for rate-limit Note, got %v", err)
	}
}

func TestAlphaVantageInformationQuota(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Information":"Your daily limit has been reached."}`))
	}))

	_, err := a.Fetch(context.Background(), "SPX")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream for Information quota, got %v", err)
	}
}

func TestAlphaVantageExplicitError(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Error Message":"Invalid API call. Please retry or visit the documentation."}`))
	}))

	_, err := a.Fetch(context.Background(), "FOO")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData for Error Message, got %v", err)
	}
}

func TestAlphaVantageRequiresAPIKey(t *testing.T) {
	_, err := NewAlphaVantage(config.SourceConfig{Enabled: true, BaseURL: "http://example.com", Timeout: "2s"})
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("expected ErrConfig for missing api key, got %v", err)
	}
}

func TestAlphaVantageEmptyExchange(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Realtime Currency Exchange Rate":{}}`))
	}))

	_, err := a.Fetch(context.Background(), "XAU")
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("expected ErrNoData, got %v", err)
	}
}
