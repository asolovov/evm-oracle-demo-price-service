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

// Yahoo adapter — Yahoo Finance v8 chart endpoint, no auth. Covers all RWA:
// gold (GC=F), silver (SI=F), copper (HG=F), WTI (CL=F), S&P 500 (^GSPC).
//
// UNOFFICIAL endpoint: no ToS grant for programmatic use; it can change or
// add cookie/crumb gating without notice. Never the sole source for an
// asset. Commodity tickers are front-month futures (small basis vs spot).
type Yahoo struct {
	*baseClient
}

// NewYahoo constructs the adapter from per-source config. No API key.
func NewYahoo(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: yahoo.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: yahoo.base_url is required", ErrConfig)
	}
	return &Yahoo{
		baseClient: newBaseClient(
			models.SourceYahoo,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceYahoo.
func (y *Yahoo) Kind() models.SourceKind { return models.SourceYahoo }

type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
				Currency           string  `json:"currency"`
			} `json:"meta"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// Fetch retrieves the latest price for a Yahoo ticker (e.g. GC=F, ^GSPC).
func (y *Yahoo) Fetch(ctx context.Context, ticker string) (models.RawPrice, error) {
	if err := y.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	endpoint := fmt.Sprintf("%s/v8/finance/chart/%s", y.baseURL, url.PathEscape(ticker))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")
	// Yahoo rejects requests with no/blank User-Agent.
	req.Header.Set("User-Agent", "evm-oracle-demo-price-service/1.0")

	now := time.Now().UTC()
	resp, err := y.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %w", ErrUpstream, err)
	}
	// 404 = unknown ticker (Yahoo returns a chart.error body with 404).
	if resp.StatusCode == http.StatusNotFound {
		return models.RawPrice{}, fmt.Errorf("%w: yahoo 404 for %q", ErrNoData, ticker)
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(body, 256))
	}

	var parsed yahooChartResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	if parsed.Chart.Error != nil {
		return models.RawPrice{}, fmt.Errorf("%w: yahoo error for %q: %s", ErrNoData, ticker, parsed.Chart.Error.Description)
	}
	if len(parsed.Chart.Result) == 0 {
		return models.RawPrice{}, fmt.Errorf("%w: yahoo returned no result for %q", ErrNoData, ticker)
	}
	meta := parsed.Chart.Result[0].Meta
	if meta.RegularMarketPrice <= 0 {
		return models.RawPrice{}, fmt.Errorf("%w: yahoo non-positive price for %q", ErrNoData, ticker)
	}

	observed := now
	if meta.RegularMarketTime > 0 {
		observed = time.Unix(meta.RegularMarketTime, 0).UTC()
	}
	return models.RawPrice{
		Source:           models.SourceYahoo,
		Price:            meta.RegularMarketPrice,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}
