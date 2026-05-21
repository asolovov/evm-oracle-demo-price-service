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

// CoinGecko adapter — public /simple/price endpoint, no auth.
//
// Free-tier rate limit: ~10-30 req/min. We default to 0.4 req/s (24 req/min)
// with a burst of 2; ops can lower further via SOURCES_COINGECKO_RATE_LIMIT.
type CoinGecko struct {
	*baseClient
}

// NewCoinGecko constructs the adapter from per-source config.
func NewCoinGecko(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: coingecko.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: coingecko.base_url is required", ErrConfig)
	}
	return &CoinGecko{
		baseClient: newBaseClient(
			models.SourceCoinGecko,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceCoinGecko.
func (c *CoinGecko) Kind() models.SourceKind { return models.SourceCoinGecko }

// coinGeckoResp is keyed by the CoinGecko asset id (e.g. "weth").
type coinGeckoResp map[string]struct {
	USD             float64 `json:"usd"`
	LastUpdatedUnix int64   `json:"last_updated_at"`
}

// Fetch retrieves the current USD price for the given CoinGecko id.
func (c *CoinGecko) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := c.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	q := url.Values{}
	q.Set("ids", symbol)
	q.Set("vs_currencies", "usd")
	q.Set("include_last_updated_at", "true")

	endpoint := fmt.Sprintf("%s/api/v3/simple/price?%s", c.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		// Pro plan key; free tier does not require it.
		req.Header.Set("x-cg-pro-api-key", c.apiKey)
	}

	now := time.Now().UTC()
	resp, err := c.httpClient.Do(req)
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

	var parsed coinGeckoResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	entry, ok := parsed[symbol]
	if !ok {
		return models.RawPrice{}, fmt.Errorf("%w: coingecko did not return id %q", ErrNoData, symbol)
	}
	if entry.USD == 0 {
		return models.RawPrice{}, fmt.Errorf("%w: coingecko returned zero price for %q", ErrNoData, symbol)
	}

	observed := now
	if entry.LastUpdatedUnix > 0 {
		observed = time.Unix(entry.LastUpdatedUnix, 0).UTC()
	}

	return models.RawPrice{
		Source:           models.SourceCoinGecko,
		Price:            entry.USD,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}

// truncate trims an error payload so we don't dump megabytes into logs when
// an upstream misbehaves.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
