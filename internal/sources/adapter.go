// Package sources holds the per-source adapters and a small Registry that
// the aggregator queries.
//
// Each adapter satisfies the Adapter interface. The aggregator passes the
// asset's source-specific symbol (resolved via Asset.SymbolFor) and an
// errgroup-managed context; adapters are responsible only for the network
// call + parse + rate limiting.
package sources

import (
	"context"
	"errors"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Adapter fetches a single price observation from one source.
//
// Implementations MUST:
//   - Honour ctx cancellation (every blocking call accepts a context).
//   - Respect the internal rate limit (use AcquireToken below).
//   - Return ErrNoData when the upstream responds but does not carry a
//     usable price (out-of-hours RWA, asset not listed by the source).
//   - Return ErrUpstream wrapped with the underlying cause for network /
//     decode / 5xx failures so the aggregator can drop the source for this
//     round without blowing up the whole tick.
//   - Set RawPrice.Source to a stable SourceKind; the field is canonical.
//   - Populate FetchedAt with time.Now() inside the adapter, and
//     SourceObservedAt with the upstream-reported timestamp where available
//     (falling back to FetchedAt for sources that don't report one).
type Adapter interface {
	// Kind returns the SourceKind discriminator for this adapter. Used in
	// logs, metrics, and the source breakdown column.
	Kind() models.SourceKind

	// Fetch retrieves the current price for the given source-specific symbol.
	// Symbol is whatever AssetConfig.Symbols holds for this source (e.g.
	// "weth" for CoinGecko, "ETHUSDT" for Binance, a pool address for
	// Uniswap V3, "XAU" for Alpha Vantage).
	Fetch(ctx context.Context, symbol string) (models.RawPrice, error)
}

// Errors returned by adapters. Aggregator unwraps via errors.Is/As; do not
// redefine these in adapter packages.
var (
	// ErrNoData is returned when the upstream replied successfully but the
	// asset is unlisted, out-of-hours, or otherwise has no current price.
	// The aggregator treats this as "drop the source for this tick" rather
	// than a hard error.
	ErrNoData = errors.New("source returned no data")

	// ErrUpstream wraps transport / 5xx / decode failures. Callers should
	// pair this with the underlying cause via fmt.Errorf("%w: %v", ErrUpstream, cause).
	ErrUpstream = errors.New("upstream source error")

	// ErrConfig is returned by NewSource constructors when configuration is
	// missing or invalid (e.g. required API key absent).
	ErrConfig = errors.New("adapter misconfiguration")
)

// Common HTTP scaffolding shared by every adapter. Adapters embed a
// *baseClient to inherit timeouts, rate limiting, and a logging-aware
// HTTP client without each duplicating the same wiring.
type baseClient struct {
	httpClient *http.Client
	limiter    *rate.Limiter
	baseURL    string
	apiKey     string
	kind       models.SourceKind
}

// newBaseClient wires the shared scaffolding once per adapter. timeout must
// be > 0; rateLimit (tokens/second) <= 0 disables the limiter entirely
// (only used by tests with httptest fixtures).
func newBaseClient(kind models.SourceKind, baseURL, apiKey string, timeout time.Duration, rateLimit float64, burst int) *baseClient {
	bc := &baseClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		kind:       kind,
		httpClient: &http.Client{Timeout: timeout},
	}
	if rateLimit > 0 {
		if burst < 1 {
			burst = 1
		}
		bc.limiter = rate.NewLimiter(rate.Limit(rateLimit), burst)
	}
	return bc
}

// acquireToken blocks until the rate limiter releases or ctx is cancelled.
// No-op when limiter is nil.
func (b *baseClient) acquireToken(ctx context.Context) error {
	if b.limiter == nil {
		return nil
	}
	return b.limiter.Wait(ctx)
}
