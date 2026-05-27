package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// UniswapV3 adapter — queries The Graph's Uniswap V3 subgraph for a
// configured pool, then returns the price of the non-stable token in USD.
//
// Symbol is the pool address (0x-prefixed, lowercase). The adapter resolves
// token0/token1 + token0Price/token1Price from the subgraph and picks the
// price whose denominator is a recognized USD stablecoin (USDC/USDT/DAI).
//
// Requires a Graph Gateway API key; the URL is
// https://gateway.thegraph.com/api/<API_KEY>/subgraphs/id/<SUBGRAPH_ID>.
type UniswapV3 struct {
	*baseClient
	subgraphID string
}

// UniswapV3SubgraphID is the deployed subgraph id for Messari's Uniswap V3
// Ethereum subgraph. Pinned here so config doesn't have to expose it — every
// pool the demo uses lives on that subgraph.
const UniswapV3SubgraphID = "ESdrTJ3twMwWVoQ1hUE2u7PugEHX3QkenudD6aXCkDQ4"

// NewUniswapV3 constructs the adapter from per-source config. Requires a
// non-empty APIKey (Graph Gateway). Returns ErrConfig otherwise.
func NewUniswapV3(cfg config.SourceConfig) (Adapter, error) {
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("%w: uniswap_v3.timeout: %w", ErrConfig, err)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: uniswap_v3.base_url is required", ErrConfig)
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: uniswap_v3.api_key is required", ErrConfig)
	}
	return &UniswapV3{
		baseClient: newBaseClient(
			models.SourceUniswapV3,
			cfg.BaseURL,
			cfg.APIKey,
			timeout,
			cfg.RateLimit,
			cfg.Burst,
		),
		subgraphID: UniswapV3SubgraphID,
	}, nil
}

// Kind returns SourceUniswapV3.
func (u *UniswapV3) Kind() models.SourceKind { return models.SourceUniswapV3 }

type uniswapV3PoolToken struct {
	Symbol string `json:"symbol"`
}

type uniswapV3Pool struct {
	Token0      uniswapV3PoolToken `json:"token0"`
	Token1      uniswapV3PoolToken `json:"token1"`
	Token0Price string             `json:"token0Price"`
	Token1Price string             `json:"token1Price"`
}

type uniswapV3Resp struct {
	Data struct {
		Pool *uniswapV3Pool `json:"pool"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// usdStablecoins is the set of token symbols this adapter treats as
// 1-USD pegged for the purposes of deriving the non-stable side's USD price.
// Symbols (not addresses) keep the check pool-agnostic.
var usdStablecoins = map[string]struct{}{
	"USDC": {},
	"USDT": {},
	"DAI":  {},
	"BUSD": {},
}

const uniswapV3Query = `query PriceByPool($pool: ID!) {
  pool(id: $pool) {
    token0 { symbol }
    token1 { symbol }
    token0Price
    token1Price
  }
}`

// Fetch posts the GraphQL query for the pool and returns the USD price of
// the non-stable side.
func (u *UniswapV3) Fetch(ctx context.Context, poolAddress string) (models.RawPrice, error) {
	if err := u.acquireToken(ctx); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: rate-limit wait: %w", ErrUpstream, err)
	}

	body, err := json.Marshal(map[string]any{
		"query":     uniswapV3Query,
		"variables": map[string]string{"pool": strings.ToLower(poolAddress)},
	})
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: marshal request: %w", ErrUpstream, err)
	}

	endpoint := fmt.Sprintf("%s/%s/subgraphs/id/%s", u.baseURL, u.apiKey, u.subgraphID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: build request: %w", ErrUpstream, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	now := time.Now().UTC()
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: read body: %w", ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		return models.RawPrice{}, fmt.Errorf("%w: http %d: %s", ErrUpstream, resp.StatusCode, truncate(raw, 256))
	}

	var parsed uniswapV3Resp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: decode body: %w", ErrUpstream, err)
	}
	if len(parsed.Errors) > 0 {
		return models.RawPrice{}, fmt.Errorf("%w: graph errors: %s", ErrUpstream, parsed.Errors[0].Message)
	}
	if parsed.Data.Pool == nil {
		return models.RawPrice{}, fmt.Errorf("%w: subgraph returned no pool for %q", ErrNoData, poolAddress)
	}

	pool := parsed.Data.Pool
	s0 := strings.ToUpper(pool.Token0.Symbol)
	s1 := strings.ToUpper(pool.Token1.Symbol)
	_, s0Stable := usdStablecoins[s0]
	_, s1Stable := usdStablecoins[s1]

	var priceStr string
	switch {
	case s0Stable && !s1Stable:
		// token0Price is "token0 per token1" — i.e. USDC per WETH, which is
		// exactly the USD price of token1 (the non-stable asset).
		priceStr = pool.Token0Price
	case s1Stable && !s0Stable:
		// token1Price is "token1 per token0" — USDC per the non-stable
		// token0, again the USD price.
		priceStr = pool.Token1Price
	case s0Stable && s1Stable:
		return models.RawPrice{}, fmt.Errorf("%w: pool %s is stable/stable (%s/%s)", ErrNoData, poolAddress, s0, s1)
	default:
		return models.RawPrice{}, fmt.Errorf("%w: pool %s has no USD stablecoin side (%s/%s)", ErrNoData, poolAddress, s0, s1)
	}

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return models.RawPrice{}, fmt.Errorf("%w: parse price %q: %w", ErrUpstream, priceStr, err)
	}
	if price <= 0 {
		return models.RawPrice{}, fmt.Errorf("%w: uniswap_v3 returned non-positive price for %s", ErrNoData, poolAddress)
	}

	// Subgraph data is indexed at the chain head; SourceObservedAt is the
	// current wall-clock since the subgraph doesn't expose a per-pool
	// updated_at field directly on this query.
	return models.RawPrice{
		Source:           models.SourceUniswapV3,
		Price:            price,
		FetchedAt:        now,
		SourceObservedAt: now,
		RawPayload:       raw,
	}, nil
}
