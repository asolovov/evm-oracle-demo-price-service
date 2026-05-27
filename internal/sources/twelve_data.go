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

// TwelveData adapter — hits the public /price endpoint for the configured
// symbol (e.g. "XAU/USD", "COPPER", "SPX").
//
// The API returns either {"price": "..."} on success or
// {"code": 400, "message": "...", "status": "error"} on a bad symbol. The
// adapter maps the error envelope to ErrNoData when the upstream rejected
// the symbol; transport / 5xx / decode errors are ErrUpstream.
type TwelveData struct {
	*baseClient
}

// NewTwelveData constructs the adapter. APIKey is required.
func NewTwelveData(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: twelve_data.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: twelve_data.base_url is required", ErrConfig)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: twelve_data.api_key is required", ErrConfig)
	}
	return &TwelveData{
		baseClient: newBaseClient(
			models.SourceTwelveData,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceTwelveData.
func (t *TwelveData) Kind() models.SourceKind { return models.SourceTwelveData }

type twelveDataResp struct {
	Price   string `json:"price"`
	Status  string `json:"status"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Fetch retrieves the current price for the given Twelve Data symbol.
func (t *TwelveData) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := t.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("apikey", t.apiKey)

	endpoint := fmt.Sprintf("%s/price?%s", t.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := t.httpClient.Do(req)
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

	var parsed twelveDataResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	// Twelve Data returns 200 OK with status="error" for unknown symbols and
	// for quota exhaustion. Codes in the 4xx range are "user" issues
	// (symbol / auth) — map to ErrNoData so the aggregator drops the source
	// for this tick without giving up on the service entirely.
	if parsed.Status == "error" {
		if parsed.Code >= 400 && parsed.Code < 500 {
			return models.RawPrice{}, fmt.Errorf("%w: twelve_data: %s", ErrNoData, parsed.Message)
		}
		return models.RawPrice{}, fmt.Errorf("%w: twelve_data: %s", ErrUpstream, parsed.Message)
	}
	if parsed.Price == "" {
		return models.RawPrice{}, fmt.Errorf("%w: twelve_data returned empty price for %q", ErrNoData, symbol)
	}
	price, err := strconv.ParseFloat(parsed.Price, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse price %q: %w", ErrUpstream, parsed.Price, err)
	}
	if price == 0 {
		return models.RawPrice{}, fmt.Errorf("%w: twelve_data returned zero price for %q", ErrNoData, symbol)
	}

	// /price has no timestamp field; SourceObservedAt falls back to now.
	return models.RawPrice{
		Source:           models.SourceTwelveData,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: now,
		RawPayload:       body,
	}, nil
}
