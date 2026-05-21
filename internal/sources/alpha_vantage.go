package sources

import (
	"context"
	"encoding/json"
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

// AlphaVantage adapter — fetches USD spot rates / commodities prices.
//
// For metals (XAU, XAG) and FX-like commodities it hits
// CURRENCY_EXCHANGE_RATE; for index/commodity series (SPX, WTI, HG) it
// returns the latest close from GLOBAL_QUOTE. Both are free-tier endpoints
// (5 req/min, 500 req/day). Symbol shape matches AssetConfig.
//
// The adapter inspects the symbol to choose the right query function.
type AlphaVantage struct {
	*baseClient
}

// NewAlphaVantage constructs the adapter. APIKey is required.
func NewAlphaVantage(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: alpha_vantage.timeout: %v", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: alpha_vantage.base_url is required", ErrConfig)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: alpha_vantage.api_key is required", ErrConfig)
	}
	return &AlphaVantage{
		baseClient: newBaseClient(
			models.SourceAlphaVantage,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
	}, nil
}

// Kind returns SourceAlphaVantage.
func (a *AlphaVantage) Kind() models.SourceKind { return models.SourceAlphaVantage }

type alphaVantageFXResp struct {
	Rate struct {
		FromCurrency string `json:"1. From_Currency Code"`
		ToCurrency   string `json:"3. To_Currency Code"`
		Exchange     string `json:"5. Exchange Rate"`
		LastRefresh  string `json:"6. Last Refreshed"`
		TimeZone     string `json:"7. Time Zone"`
	} `json:"Realtime Currency Exchange Rate"`
	Note     string `json:"Note"`
	Info     string `json:"Information"`
	ErrorMsg string `json:"Error Message"`
}

type alphaVantageQuoteResp struct {
	Quote struct {
		Symbol        string `json:"01. symbol"`
		Price         string `json:"05. price"`
		LatestDay     string `json:"07. latest trading day"`
	} `json:"Global Quote"`
	Note     string `json:"Note"`
	Info     string `json:"Information"`
	ErrorMsg string `json:"Error Message"`
}

// metalsAndFX is the set of symbols this adapter queries via
// CURRENCY_EXCHANGE_RATE; everything else goes through GLOBAL_QUOTE.
var metalsAndFX = map[string]struct{}{
	"XAU": {},
	"XAG": {},
}

// Fetch retrieves the latest price for the given Alpha Vantage symbol.
func (a *AlphaVantage) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	if err := a.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %v", ErrUpstream, err)
	}

	upperSym := strings.ToUpper(symbol)
	if _, isFX := metalsAndFX[upperSym]; isFX {
		return a.fetchFX(ctx, upperSym)
	}
	return a.fetchQuote(ctx, upperSym)
}

func (a *AlphaVantage) fetchFX(ctx context.Context, symbol string) (models.RawPrice, error) {
	q := url.Values{}
	q.Set("function", "CURRENCY_EXCHANGE_RATE")
	q.Set("from_currency", symbol)
	q.Set("to_currency", "USD")
	q.Set("apikey", a.apiKey)

	body, status, err := a.do(ctx, q)
	if err != nil {
		return models.RawPrice{}, err
	}
	if status != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, status, truncate(body, 256))
	}

	var parsed alphaVantageFXResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %v", ErrUpstream, err)
	}
	if err := alphaVantageError(parsed.Note, parsed.Info, parsed.ErrorMsg); err != nil {
		return models.RawPrice{}, err
	}
	if parsed.Rate.Exchange == "" {
		return models.RawPrice{}, fmt.Errorf("%w: empty exchange rate for %q", ErrNoData, symbol)
	}
	price, err := strconv.ParseFloat(parsed.Rate.Exchange, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse rate %q: %v", ErrUpstream, parsed.Rate.Exchange, err)
	}
	now := time.Now().UTC()
	observed := now
	if parsed.Rate.LastRefresh != "" {
		// Alpha Vantage reports "2026-05-21 18:30:00" or similar. Best-effort
		// parse — fall back to now on failure.
		if t, perr := time.Parse("2006-01-02 15:04:05", parsed.Rate.LastRefresh); perr == nil {
			observed = t.UTC()
		}
	}
	return models.RawPrice{
		Source:           models.SourceAlphaVantage,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}

func (a *AlphaVantage) fetchQuote(ctx context.Context, symbol string) (models.RawPrice, error) {
	q := url.Values{}
	q.Set("function", "GLOBAL_QUOTE")
	q.Set("symbol", symbol)
	q.Set("apikey", a.apiKey)

	body, status, err := a.do(ctx, q)
	if err != nil {
		return models.RawPrice{}, err
	}
	if status != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, status, truncate(body, 256))
	}

	var parsed alphaVantageQuoteResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %v", ErrUpstream, err)
	}
	if err := alphaVantageError(parsed.Note, parsed.Info, parsed.ErrorMsg); err != nil {
		return models.RawPrice{}, err
	}
	if parsed.Quote.Price == "" {
		return models.RawPrice{}, fmt.Errorf("%w: empty quote price for %q", ErrNoData, symbol)
	}
	price, err := strconv.ParseFloat(parsed.Quote.Price, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse price %q: %v", ErrUpstream, parsed.Quote.Price, err)
	}
	now := time.Now().UTC()
	observed := now
	if parsed.Quote.LatestDay != "" {
		if t, perr := time.Parse("2006-01-02", parsed.Quote.LatestDay); perr == nil {
			observed = t.UTC()
		}
	}
	return models.RawPrice{
		Source:           models.SourceAlphaVantage,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: observed,
		RawPayload:       body,
	}, nil
}

func (a *AlphaVantage) do(ctx context.Context, q url.Values) ([]byte, int, error) {
	endpoint := fmt.Sprintf("%s/query?%s", a.baseURL, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: build request: %v", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: read body: %v", ErrUpstream, err)
	}
	return body, resp.StatusCode, nil
}

// alphaVantageError surfaces rate-limit / quota / explicit-error responses
// which Alpha Vantage returns as 200 OK with a top-level Note / Information
// field instead of a non-2xx status. Rate-limit responses are ErrUpstream
// (transient); explicit error messages map to ErrNoData (asset not listed).
func alphaVantageError(note, info, errMsg string) error {
	if note != "" {
		return fmt.Errorf("%w: alpha_vantage note: %s", ErrUpstream, note)
	}
	if info != "" {
		return fmt.Errorf("%w: alpha_vantage info: %s", ErrUpstream, info)
	}
	if errMsg != "" {
		return fmt.Errorf("%w: alpha_vantage error: %s", ErrNoData, errMsg)
	}
	return nil
}
