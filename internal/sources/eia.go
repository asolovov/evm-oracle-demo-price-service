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

// EIA adapter — US Energy Information Administration Open Data v2. Free key.
// Serves WTI crude daily spot via the petroleum spot-price dataset; the
// symbol is the EIA series id (e.g. "RWTC" = WTI Cushing).
//
// Cadence: DAILY spot with a multi-business-day reporting lag — not
// real-time. Official US-gov source, very stable, generous limits.
type EIA struct {
	*baseClient
}

// NewEIA constructs the adapter. APIKey is required (free, no card:
// https://www.eia.gov/opendata/register.php).
func NewEIA(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: eia.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: eia.base_url is required", ErrConfig)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: eia.api_key is required", ErrConfig)
	}
	return &EIA{
		baseClient: newBaseClient(
			models.SourceEIA,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceEIA.
func (e *EIA) Kind() models.SourceKind { return models.SourceEIA }

type eiaResp struct {
	Response struct {
		Data []struct {
			Period string  `json:"period"`
			Value  float64 `json:"value"`
		} `json:"data"`
	} `json:"response"`
}

// Fetch retrieves the latest daily spot value for an EIA petroleum series id.
func (e *EIA) Fetch(ctx context.Context, series string) (models.RawPrice, error) {
	if err := e.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	// Bracketed query keys are EIA's documented form; build the string
	// directly (url.Values would reorder/encode in ways that work but read
	// poorly) and escape only the user-supplied series + key.
	endpoint := fmt.Sprintf(
		"%s/v2/petroleum/pri/spt/data/?frequency=daily&data[]=value"+
			"&facets[series][]=%s&sort[0][column]=period&sort[0][direction]=desc"+
			"&length=1&api_key=%s",
		e.baseURL, url.QueryEscape(series), url.QueryEscape(e.apiKey),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := e.httpClient.Do(req)
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

	var parsed eiaResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	if len(parsed.Response.Data) == 0 {
		return models.RawPrice{}, fmt.Errorf("%w: eia returned no rows for series %q", ErrNoData, series)
	}
	row := parsed.Response.Data[0]
	if row.Value <= 0 {
		return models.RawPrice{}, fmt.Errorf("%w: eia non-positive value for series %q", ErrNoData, series)
	}

	observed := now
	if row.Period != "" {
		if t, perr := time.Parse("2006-01-02", row.Period); perr == nil {
			observed = t.UTC()
		}
	}
	return models.RawPrice{
		Source:           models.SourceEIA,
		Price:            row.Value,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}
