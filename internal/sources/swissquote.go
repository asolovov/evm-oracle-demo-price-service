package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Swissquote adapter — public BBO quotes feed from Swissquote Bank, no auth.
// Serves precious metals (XAU, XAG) priced in USD; the symbol is the metal
// code (e.g. "XAU"), queried against the .../instrument/{SYM}/USD path.
//
// UNOFFICIAL endpoint (the feed behind their public site): no documented ToS
// for programmatic use and it can change without notice. Run by a regulated
// bank, so reasonably stable, but never the sole source for an asset. The
// returned price is the mid of the first spread profile's bid/ask.
type Swissquote struct {
	*baseClient
}

// NewSwissquote constructs the adapter from per-source config. No API key.
func NewSwissquote(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: swissquote.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: swissquote.base_url is required", ErrConfig)
	}
	return &Swissquote{
		baseClient: newBaseClient(
			models.SourceSwissquote,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceSwissquote.
func (s *Swissquote) Kind() models.SourceKind { return models.SourceSwissquote }

type swissquoteQuote struct {
	SpreadProfilePrices []struct {
		SpreadProfile string  `json:"spreadProfile"`
		Bid           float64 `json:"bid"`
		Ask           float64 `json:"ask"`
	} `json:"spreadProfilePrices"`
}

// Fetch retrieves the current USD mid price for a metal symbol (XAU/XAG).
func (s *Swissquote) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := s.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	endpoint := fmt.Sprintf("%s/public-quotes/bboquotes/instrument/%s/USD", s.baseURL, url.PathEscape(symbol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %w", ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(body, 256))
	}

	var quotes []swissquoteQuote
	if err := json.Unmarshal(body, &quotes); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	for _, qt := range quotes {
		for _, p := range qt.SpreadProfilePrices {
			if p.Bid > 0 && p.Ask > 0 {
				mid := (p.Bid + p.Ask) / 2
				return models.RawPrice{
					Source:           models.SourceSwissquote,
					Price:            mid,
					FetchedAt:        now,
					SourceObservedAt: now, // feed carries no per-quote timestamp
					RawPayload:       body,
				}, nil
			}
		}
	}
	return models.RawPrice{}, fmt.Errorf("%w: swissquote returned no usable bid/ask for %q", ErrNoData, symbol)
}
