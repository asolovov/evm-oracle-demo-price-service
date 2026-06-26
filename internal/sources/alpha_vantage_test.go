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

// Non-metal symbols are rejected by the allowlist guard — they must NEVER
// fall through to GLOBAL_QUOTE (which is equities-only and returns the wrong
// instrument). The HTTP server must not even be hit.
func TestAlphaVantageRejectsNonMetalSymbol(t *testing.T) {
	for _, sym := range []string{"SPX", "WTI", "HG"} {
		t.Run(sym, func(t *testing.T) {
			a := newTestAlphaVantage(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Fatalf("adapter must not make an HTTP call for non-metal symbol %q", sym)
			}))
			_, err := a.Fetch(context.Background(), sym)
			if !errors.Is(err, ErrConfig) {
				t.Fatalf("Fetch(%q): expected ErrConfig (allowlist rejection), got %v", sym, err)
			}
		})
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

	// Information/Note quota envelope is surfaced on the FX (metals) path.
	_, err := a.Fetch(context.Background(), "XAG")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected ErrUpstream for Information quota, got %v", err)
	}
}

func TestAlphaVantageExplicitError(t *testing.T) {
	a := newTestAlphaVantage(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Error Message":"Invalid API call. Please retry or visit the documentation."}`))
	}))

	// "Error Message" on the FX path → ErrNoData (asset not listed).
	_, err := a.Fetch(context.Background(), "XAU")
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
