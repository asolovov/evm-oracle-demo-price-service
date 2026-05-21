# Source adapters

Per-source detail for the six adapters under `internal/sources/`. Covers
how the free-tier surface works, what the adapter sends, what shape it
expects back, how to obtain a key, and the per-source freshness window the
aggregator's `strict` mode would gate on.

All six implement the same interface:

```go
type Adapter interface {
    Kind() models.SourceKind
    Fetch(ctx context.Context, symbol string) (models.RawPrice, error)
}
```

Errors are normalised: `ErrNoData` for "upstream replied but no usable
price" (asset unlisted, out-of-hours, quota for free tier), `ErrUpstream`
for transport / 5xx / decode, `ErrConfig` for constructor failures.

---

## CoinGecko

- **Endpoint:** `GET https://api.coingecko.com/api/v3/simple/price?ids=<id>&vs_currencies=usd&include_last_updated_at=true`
- **Auth:** None for the public demo tier.
- **Symbol shape:** CoinGecko id, lowercase (`weth`, `wrapped-bitcoin`,
  `chainlink`, `uniswap`, `aave`).
- **Free-tier rate limit:** ~10–30 req/min (public). Default
  `rate_limit=0.4` → 24 req/min with `burst=2`.
- **Observation time:** `last_updated_at` (unix seconds).
- **Failure modes:** 429 (rate limit) → `ErrUpstream`; 200 with `{}` (id
  not recognised) → `ErrNoData`; zero price → `ErrNoData`.

## Binance

- **Endpoint:** `GET https://api.binance.com/api/v3/ticker/price?symbol=<ticker>`
- **Auth:** None.
- **Symbol shape:** Binance ticker (`ETHUSDT`, `WBTCUSDT`, `LINKUSDT`,
  `UNIUSDT`, `AAVEUSDT`).
- **Free-tier rate limit:** 1200 req/min (per IP). Default
  `rate_limit=5.0` (300 req/min) with `burst=10`.
- **Observation time:** Binance ticker has no `observed_at`; falls back
  to `fetched_at`.
- **Failure modes:** 400 with code `-1121` ("invalid symbol") → `ErrNoData`;
  other non-2xx → `ErrUpstream`.

## Uniswap V3

- **Endpoint:** GraphQL POST `https://gateway.thegraph.com/api/<API_KEY>/subgraphs/id/<SUBGRAPH_ID>`
- **Auth:** Graph Gateway API key. Sign up at
  [thegraph.com/studio](https://thegraph.com/studio/), connect a wallet,
  create an API key. Free tier: 100K queries/month.
- **Symbol shape:** Pool address, 0x-prefixed lowercase hex.
- **Subgraph:** Messari's Uniswap V3 Ethereum subgraph; ID pinned in
  `internal/sources/uniswap_v3.go` as `UniswapV3SubgraphID`.
- **Pricing logic:** Query returns `token0.symbol`, `token1.symbol`,
  `token0Price`, `token1Price`. The adapter identifies the USD-stablecoin
  side (USDC / USDT / DAI / BUSD) and returns the non-stable side's USD
  price.
- **Observation time:** Not exposed by the query; falls back to
  `fetched_at`.
- **Failure modes:** Gateway 4xx → `ErrUpstream`; GraphQL `errors` array
  populated → `ErrUpstream`; pool returns `null` (unknown id) → `ErrNoData`;
  pool is stable/stable or has no recognised stable side → `ErrNoData`.

## Alpha Vantage

- **Endpoint family:**
  - For XAU / XAG: `GET /query?function=CURRENCY_EXCHANGE_RATE&from_currency=<sym>&to_currency=USD&apikey=<key>`
  - For SPX / WTI / HG: `GET /query?function=GLOBAL_QUOTE&symbol=<sym>&apikey=<key>`
- **Auth:** API key. Sign up at
  [alphavantage.co](https://www.alphavantage.co/support/#api-key) — no
  email verification needed; key returned immediately.
- **Symbol shape:** Free-text symbol (`XAU`, `XAG`, `SPX`, `WTI`, `HG`).
- **Free-tier rate limit:** 25 requests/day, 5/minute. Default
  `rate_limit=0.08` (4.8 req/min) with `burst=1`. The 25/day ceiling is
  the real constraint — RWA polls every 6 h → 4 polls/asset/day × 5 RWA
  assets = 20/day, leaving headroom.
- **Observation time:** `Last Refreshed` (FX) or `latest trading day`
  (Global Quote).
- **Failure modes:** Alpha Vantage returns 200 OK with a top-level `Note`
  (rate limit) or `Information` (quota) field even on error. The adapter
  surfaces these as `ErrUpstream` so the aggregator drops the source for
  the tick. Explicit `Error Message` (unknown symbol) → `ErrNoData`.

## Twelve Data

- **Endpoint:** `GET https://api.twelvedata.com/price?symbol=<sym>&apikey=<key>`
- **Auth:** API key. Sign up at [twelvedata.com](https://twelvedata.com/)
  via email or Google.
- **Symbol shape:** Twelve Data format (`XAU/USD`, `XAG/USD`, `SPX`,
  `WTI/USD`, `COPPER`).
- **Free-tier rate limit:** 800 req/day, 8/minute. Default
  `rate_limit=0.13` (~8 req/min) with `burst=2`.
- **Observation time:** Not exposed by `/price`; falls back to
  `fetched_at`.
- **Failure modes:** 200 OK with `status="error"` + 4xx code (bad symbol,
  exhausted quota) → `ErrNoData`; `status="error"` + 5xx code →
  `ErrUpstream`; empty price → `ErrNoData`.

## Stooq

- **Endpoint:** `GET https://stooq.com/q/l/?s=<sym>&f=sd2t2ohlcv&h&e=csv`
- **Auth:** None.
- **Symbol shape:** Stooq ticker, lowercase (`xauusd`, `xagusd`, `^spx`,
  `cl.f`, `hg.f`). The adapter lowercases automatically.
- **Free-tier rate limit:** Undocumented; conservative default
  `rate_limit=1.0` (60 req/min) with `burst=2`.
- **Observation time:** Parsed from CSV `Date` + `Time` columns.
- **Failure modes:** Non-2xx → `ErrUpstream`; CSV parse error →
  `ErrUpstream`; missing `Close` column or `N/D` value → `ErrNoData`.

---

## Troubleshooting

### "alpha_vantage note: ..."

The free tier serves 25 requests/day. If a polling cycle exhausts that
budget, every subsequent fetch returns 200 OK with a top-level `Note`. The
adapter classifies this as `ErrUpstream`; the aggregator drops the source
for the tick. Reduce `SOURCES_ALPHA_VANTAGE_RATE_LIMIT` or disable the
source temporarily if you need quota for elsewhere.

### "graph errors: indexer ... is not synced"

The Graph Gateway sometimes routes to an indexer that has fallen behind
the chain head. Retrying usually resolves; if it persists, switch the
subgraph ID (see `UniswapV3SubgraphID`) or pin to a specific indexer via
the gateway's deployment query params.

### Aggregator logs "tick asset=<id>: not enough sources"

Two failures-in-a-row push the included count below `MinSources`. Either
loosen `AGGREGATION_MIN_SOURCES=1` (the default), or check the per-source
logs for the actual cause (rate limit, key expired, upstream outage).
