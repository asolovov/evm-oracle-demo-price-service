package sources

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Stooq adapter — CSV /q/l/ endpoint, no auth.
//
// Returns a one-line CSV header + one data row with the latest close.
// Example response for symbol=xauusd&f=sd2t2ohlcv&h&e=csv:
//
//	Symbol,Date,Time,Open,High,Low,Close,Volume
//	XAUUSD,2026-05-21,18:45:00,2410.5,2415.0,2406.2,2412.8,0
//
// The adapter parses the Close column as the latest price.
type Stooq struct {
	*baseClient
}

// NewStooq constructs the adapter.
func NewStooq(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: stooq.timeout: %v", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: stooq.base_url is required", ErrConfig)
	}
	return &Stooq{
		baseClient: newBaseClient(
			models.SourceStooq,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceStooq.
func (s *Stooq) Kind() models.SourceKind { return models.SourceStooq }

// Fetch retrieves the latest close for the given Stooq symbol.
func (s *Stooq) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := s.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %v", ErrUpstream, err)
	}

	q := url.Values{}
	q.Set("s", strings.ToLower(symbol))
	q.Set("f", "sd2t2ohlcv")
	q.Set("h", "")
	q.Set("e", "csv")
	endpoint := fmt.Sprintf("%s/q/l/?%s", s.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %v", ErrUpstream, err)
	}
	req.Header.Set("Accept", "text/csv,text/plain")

	now := time.Now().UTC()
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %v", ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(body, 256))
	}

	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse csv: %v", ErrUpstream, err)
	}
	if len(rows) < 2 {
		return models.RawPrice{}, fmt.Errorf("%w: stooq csv had no data rows for %q", ErrNoData, symbol)
	}

	// Find column indices from the header row — order is stable per the
	// `f=sd2t2ohlcv` argument but we look them up to stay resilient.
	header := rows[0]
	dataRow := rows[1]
	idx := func(name string) int {
		for i, h := range header {
			if strings.EqualFold(h, name) {
				return i
			}
		}
		return -1
	}
	closeIdx := idx("Close")
	dateIdx := idx("Date")
	timeIdx := idx("Time")
	if closeIdx < 0 || closeIdx >= len(dataRow) {
		return models.RawPrice{}, fmt.Errorf("%w: stooq csv missing Close column", ErrUpstream)
	}

	closeRaw := strings.TrimSpace(dataRow[closeIdx])
	if closeRaw == "" || closeRaw == "N/D" {
		return models.RawPrice{}, fmt.Errorf("%w: stooq returned no close for %q", ErrNoData, symbol)
	}
	price, err := strconv.ParseFloat(closeRaw, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse close %q: %v", ErrUpstream, closeRaw, err)
	}
	if price <= 0 {
		return models.RawPrice{}, fmt.Errorf("%w: stooq returned non-positive close for %q", ErrNoData, symbol)
	}

	observed := now
	if dateIdx >= 0 && dateIdx < len(dataRow) && timeIdx >= 0 && timeIdx < len(dataRow) {
		stamp := dataRow[dateIdx] + " " + dataRow[timeIdx]
		if t, perr := time.Parse("2006-01-02 15:04:05", stamp); perr == nil {
			observed = t.UTC()
		}
	}

	return models.RawPrice{
		Source:           models.SourceStooq,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}
