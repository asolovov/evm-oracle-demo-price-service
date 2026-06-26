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

// FRED adapter — Federal Reserve Bank of St. Louis economic data API. Free
// key. Serves WTI (series DCOILWTICO) and the S&P 500 (series SP500); the
// symbol is the FRED series id.
//
// Cadence: DAILY close / business-day lag — not real-time. Non-trading days
// return value "." (blank), which this adapter skips, walking back to the
// most recent real observation.
type FRED struct {
	*baseClient
}

// NewFRED constructs the adapter. APIKey is required (free, no card:
// https://fred.stlouisfed.org/docs/api/api_key.html).
func NewFRED(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: fred.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: fred.base_url is required", ErrConfig)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: fred.api_key is required", ErrConfig)
	}
	return &FRED{
		baseClient: newBaseClient(
			models.SourceFRED,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceFRED.
func (f *FRED) Kind() models.SourceKind { return models.SourceFRED }

type fredResp struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

// fredMissingValue is FRED's sentinel for a non-trading day.
const fredMissingValue = "."

// Fetch retrieves the most recent valid observation for a FRED series id.
func (f *FRED) Fetch(ctx context.Context, series string) (models.RawPrice, error) {
	if err := f.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	q := url.Values{}
	q.Set("series_id", series)
	q.Set("api_key", f.apiKey)
	q.Set("file_type", "json")
	q.Set("sort_order", "desc")
	// Request several rows so weekends/holidays (value ".") can be skipped.
	q.Set("limit", "10")

	endpoint := fmt.Sprintf("%s/fred/series/observations?%s", f.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := f.httpClient.Do(req)
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

	var parsed fredResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}

	// Observations are desc by date; take the first non-missing value.
	for _, o := range parsed.Observations {
		if o.Value == fredMissingValue || o.Value == "" {
			continue
		}
		price, perr := strconv.ParseFloat(o.Value, 64)
		if perr != nil {
			return models.RawPrice{}, fmt.Errorf("%w: parse value %q: %w", ErrUpstream, o.Value, perr)
		}
		if price <= 0 {
			continue
		}
		observed := now
		if o.Date != "" {
			if t, derr := time.Parse("2006-01-02", o.Date); derr == nil {
				observed = t.UTC()
			}
		}
		return models.RawPrice{
			Source:           models.SourceFRED,
			Price:            price,
			FetchedAt:        now,
			SourceObservedAt: observed,
			RawPayload:       body,
		}, nil
	}
	return models.RawPrice{}, fmt.Errorf("%w: fred series %q had no valid observation in the last 10 rows", ErrNoData, series)
}
