# Source adapters

Per-source detail for the adapters under `internal/sources/`. Covers how
the free-tier surface works, what the adapter sends, what shape it expects
back, how to obtain a key, and the per-source freshness window the
aggregator's `strict` mode would gate on. Crypto: CoinGecko, Binance,
Uniswap V3. RWA: gold-api, Yahoo, EIA, FRED, Swissquote, Alpha Vantage
(metals only); Twelve Data + Stooq ship disabled (see the RWA matrix).

All adapters implement the same interface:

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

## RWA source matrix (reworked in task 05.2)

The original RWA mix (Alpha Vantage `GLOBAL_QUOTE` + Twelve Data + Stooq)
was broken — AV `GLOBAL_QUOTE` returned **equities** for SPX/WTI/HG
(ticker collisions: "WTI" → W&T Offshore stock, "HG" → an unrelated
equity), Twelve Data's free tier paywalls every RWA symbol, and Stooq's
CSV endpoint returns HTML errors. Current mapping (`config/init.go`
`defaultAssets()`):

| Asset | Sources | Cadence |
|-------|---------|---------|
| XAU (gold)   | gold-api + Yahoo `GC=F` + Alpha Vantage FX + Swissquote | real-time |
| XAG (silver) | gold-api + Yahoo `SI=F` + Alpha Vantage FX + Swissquote | real-time |
| HG (copper)  | gold-api + Yahoo `HG=F` | real-time |
| WTI (crude)  | EIA `RWTC` + FRED `DCOILWTICO` | **daily spot, lagged** |
| SPX (S&P 500)| Yahoo `^GSPC` + FRED `SP500` | mixed (Yahoo real-time + FRED daily close) |

**Cadence decision:** WTI uses two daily official-gov *spot* sources
(EIA + FRED) rather than mixing in a real-time future (Yahoo `CL=F`),
because spot-vs-future basis + multi-day lag would bias the median. SPX
unavoidably mixes Yahoo (real-time) with FRED (daily close) — no second
real-time free SPX source exists — so the SPX median can lag intraday.
Both are deliberate, documented demo trade-offs; flip the mappings in
`defaultAssets()` if running with paid real-time keys.

## gold-api.com

- **Endpoint:** `GET https://api.gold-api.com/price/<SYM>` (`XAU`, `XAG`, `HG`).
- **Auth:** None, no documented rate limit. (Distinct from the key-gated
  `goldapi.io` — do not confuse.)
- **Response / price field:** JSON `.price` (+ `.symbol`, `.updatedAt`).
- **Observation time:** `updatedAt` (RFC3339), falls back to `fetched_at`.
- **Failure modes:** 404 → `ErrNoData`; other non-2xx → `ErrUpstream`;
  non-positive price → `ErrNoData`.
- **Risk:** small operator, no SLA — always paired with ≥1 other source.

## Yahoo Finance v8 (unofficial)

- **Endpoint:** `GET https://query1.finance.yahoo.com/v8/finance/chart/<TICKER>`
  — `GC=F` (gold), `SI=F` (silver), `HG=F` (copper), `CL=F` (WTI),
  `^GSPC` (S&P 500). `^` is percent-encoded in the path.
- **Auth:** None; a non-blank `User-Agent` header is required.
- **Response / price field:** JSON `chart.result[0].meta.regularMarketPrice`
  (+ `regularMarketTime`). Commodity tickers are front-month **futures**.
- **Rate limit:** undocumented; default `rate_limit=2.0`/`burst=4`
  (~0.5s spacing). Both `query1`/`query2` hosts work.
- **Failure modes:** 404 / `chart.error` → `ErrNoData`; other non-2xx →
  `ErrUpstream`; empty result or non-positive price → `ErrNoData`.
- **Risk:** **unofficial** — no programmatic-use ToS; can change or add
  cookie/crumb gating without notice. Never the sole source for an asset.

## EIA Open Data v2

- **Endpoint:** `GET https://api.eia.gov/v2/petroleum/pri/spt/data/?frequency=daily&data[]=value&facets[series][]=<SERIES>&sort[0][column]=period&sort[0][direction]=desc&length=1&api_key=<key>`
  (`SERIES` = `RWTC` for WTI Cushing spot).
- **Auth:** Free key, instant, no card:
  [eia.gov/opendata/register](https://www.eia.gov/opendata/register.php).
  Env: `SOURCES_EIA_API_KEY`.
- **Response / price field:** JSON `response.data[0].value` (number),
  `period` (date).
- **Cadence:** **daily spot, ~2–3 business-day lag** — not real-time.
- **Rate limit:** ~9000 req/hr; default `rate_limit=5.0`/`burst=5`.
- **Failure modes:** non-2xx → `ErrUpstream`; no rows / non-positive →
  `ErrNoData`.

## FRED (St. Louis Fed)

- **Endpoint:** `GET https://api.stlouisfed.org/fred/series/observations?series_id=<SERIES>&api_key=<key>&file_type=json&sort_order=desc&limit=10`
  (`DCOILWTICO` = WTI, `SP500` = S&P 500).
- **Auth:** Free key:
  [fred.stlouisfed.org/docs/api/api_key](https://fred.stlouisfed.org/docs/api/api_key.html).
  Env: `SOURCES_FRED_API_KEY`.
- **Response / price field:** JSON `observations[].value` (string). The
  adapter requests 10 rows desc and **skips `"."` blanks** (non-trading
  days), taking the most recent real value.
- **Cadence:** **daily close, business-day lag** — not real-time.
- **Rate limit:** 120 req/min; default `rate_limit=2.0`/`burst=4`.
- **Failure modes:** non-2xx → `ErrUpstream`; all rows blank → `ErrNoData`.

## Swissquote (unofficial)

- **Endpoint:** `GET https://forex-data-feed.swissquote.com/public-quotes/bboquotes/instrument/<SYM>/USD`
  (`XAU`, `XAG`).
- **Auth:** None.
- **Response / price field:** JSON array; the adapter takes the first
  spread profile's bid/ask **mid**.
- **Risk:** unofficial public feed; reasonably stable (regulated bank) but
  can change without notice. Metals only.

## Alpha Vantage (metals only — allowlisted)

- **Endpoint:** `GET /query?function=CURRENCY_EXCHANGE_RATE&from_currency=<sym>&to_currency=USD&apikey=<key>`.
- **Auth:** API key (`SOURCES_ALPHA_VANTAGE_API_KEY`). Sign up at
  [alphavantage.co](https://www.alphavantage.co/support/#api-key).
- **Symbol shape:** `XAU`, `XAG` **only**. The adapter holds an allowlist
  (`metalsAndFX`); any other symbol returns `ErrConfig` and is **never**
  routed to `GLOBAL_QUOTE` (which is equities-only — the source of the
  original SPX/WTI/HG bug). The `GLOBAL_QUOTE` path was removed entirely.
- **Free-tier rate limit:** 25 req/day, 5/min. Default `rate_limit=0.08`
  (~4.8 req/min) with `burst=1`. 2 metals × 2 polls/day = 4/day — well
  under the cap.
- **Observation time:** `Last Refreshed`.
- **Failure modes:** 200 OK with top-level `Note` (rate limit) /
  `Information` (quota) → `ErrUpstream`; `Error Message` → `ErrNoData`;
  non-metal symbol → `ErrConfig`.

## Twelve Data & Stooq — DISABLED

Both are **disabled by default** (`sources.twelve_data.enabled=false`,
`sources.stooq.enabled=false`) and serve nothing here:

- **Twelve Data:** free tier paywalls every RWA symbol — `XAG/USD`,
  `WTI/USD`, `COPPER`, `SPX` return `404 "available starting with the
  Grow or Venture plan"`. Re-enable only with a paid key.
- **Stooq:** the CSV quote endpoint (`stooq.com/q/l/?...&e=csv`) returns
  an HTML error page for every symbol (bot/geo block).

Their adapters remain in the tree; flip `enabled=true` + supply any
required key to reactivate.

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
