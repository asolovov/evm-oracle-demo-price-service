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

// GoldAPI adapter — api.gold-api.com, no auth, no documented rate limit.
// Covers precious metals + copper: XAU, XAG, HG (also XPT/XPD).
//
// NOTE: api.gold-api.com is a distinct service from the key-gated
// goldapi.io (100 req/mo) — do not confuse them.
type GoldAPI struct {
	*baseClient
}

// NewGoldAPI constructs the adapter from per-source config. No API key.
func NewGoldAPI(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: gold_api.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: gold_api.base_url is required", ErrConfig)
	}
	return &GoldAPI{
		baseClient: newBaseClient(
			models.SourceGoldAPI,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceGoldAPI.
func (g *GoldAPI) Kind() models.SourceKind { return models.SourceGoldAPI }

type goldAPIResp struct {
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Symbol    string  `json:"symbol"`
	UpdatedAt string  `json:"updatedAt"`
}

// Fetch retrieves the current USD price for a gold-api symbol (XAU/XAG/HG).
func (g *GoldAPI) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := g.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	endpoint := fmt.Sprintf("%s/price/%s", g.baseURL, url.PathEscape(symbol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := g.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %w", ErrUpstream, err)
	}
	// Unknown symbols return 404 with an error body — treat as no data.
	if resp.StatusCode == http.StatusNotFound {
		return models.RawPrice{}, fmt.Errorf("%w: gold_api 404 for %q", ErrNoData, symbol)
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(body, 256))
	}

	var parsed goldAPIResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	if parsed.Price <= 0 {
		return models.RawPrice{}, fmt.Errorf("%w: gold_api non-positive price for %q", ErrNoData, symbol)
	}

	observed := now
	if parsed.UpdatedAt != "" {
		if t, perr := time.Parse(time.RFC3339, parsed.UpdatedAt); perr == nil {
			observed = t.UTC()
		}
	}
	return models.RawPrice{
		Source:           models.SourceGoldAPI,
		Price:            parsed.Price,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}
