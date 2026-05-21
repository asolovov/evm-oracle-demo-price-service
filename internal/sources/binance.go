package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Binance adapter — public /api/v3/ticker/price endpoint, no auth.
//
// Returns last-trade price as a string. Binance does not stamp the ticker
// with an observation time, so SourceObservedAt is set to FetchedAt.
type Binance struct {
	*baseClient
}

// NewBinance constructs the adapter from per-source config.
func NewBinance(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: binance.timeout: %v", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: binance.base_url is required", ErrConfig)
	}
	return &Binance{
		baseClient: newBaseClient(
			models.SourceBinance,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceBinance.
func (b *Binance) Kind() models.SourceKind { return models.SourceBinance }

type binanceTicker struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

// Fetch retrieves the latest price for symbol (e.g. ETHUSDT).
func (b *Binance) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := b.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %v", ErrUpstream, err)
	}

	q := url.Values{}
	q.Set("symbol", symbol)
	endpoint := fmt.Sprintf("%s/api/v3/ticker/price?%s", b.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %v", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %v", ErrUpstream, err)
	}
	// 400 with code -1121 means "Invalid symbol" — treat as ErrNoData.
	if resp.StatusCode == http.StatusBadRequest {
		return models.RawPrice{}, fmt.Errorf("%w: binance rejected symbol %q: %s", ErrNoData, symbol, truncate(body, 256))
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(body, 256))
	}

	var t binanceTicker
	if err := json.Unmarshal(body, &t); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %v", ErrUpstream, err)
	}
	if t.Price == "" {
		return models.RawPrice{}, fmt.Errorf("%w: binance returned empty price for %q", ErrNoData, symbol)
	}
	price, err := strconv.ParseFloat(t.Price, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse price %q: %v", ErrUpstream, t.Price, err)
	}
	if price == 0 {
		return models.RawPrice{}, fmt.Errorf("%w: binance returned zero price for %q", ErrNoData, symbol)
	}

	return models.RawPrice{
		Source:           models.SourceBinance,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: now,
		RawPayload:       body,
	}, nil
}
