# evm-oracle-demo · `price-service`

Off-chain price aggregation for the [evm-oracle-demo] family of services.
Polls **six** free-tier sources across **ten** assets (5 crypto + 5 RWA),
medians + deviation-guards each round, persists the raw + aggregated rows,
and serves the result over gRPC.

This service is **demo-grade** by design. See [Production gaps](#production-gaps).

---

## What it does

```
┌──────────────────────────────────┐
│  price-service                   │
│                                  │
│  ┌──────────────┐                │
│  │ scheduler    │  per-asset     │
│  │ (one go per  │  refresh tick  │
│  │  asset)      │                │
│  └──────┬───────┘                │
│         │                        │
│         ▼                        │
│  ┌──────────────┐  errgroup      │
│  │ aggregator   │  fan-out  ───► CoinGecko / Binance / Uniswap V3
│  │              │                Alpha Vantage / Twelve Data / Stooq
│  │  median +    │  freshness     │
│  │  deviation   │  policy        │
│  └──────┬───────┘                │
│         │                        │
│         ▼                        │
│  ┌──────────────┐                │
│  │ repository   │  evm_price DB  │
│  │  (pgx)       │  one tx /round │
│  └──────┬───────┘                │
│         │                        │
│         ▼                        │
│  ┌──────────────┐                │
│  │ gRPC server  │  price.v1      │
│  │              │  GetPrice +    │
│  │  + in-mem bus│  Subscribe     │
│  └──────────────┘                │
└──────────────────────────────────┘
```

- **Sources** (`internal/sources/`): six adapters behind a uniform
  `Adapter.Fetch(ctx, symbol) (RawPrice, error)`. Each has its own
  `golang.org/x/time/rate` token bucket sized for the source's free tier.
- **Aggregator** (`internal/aggregator/`): per-tick fan-out via
  `errgroup`, median, deviation guard against the cached last accepted
  price, transactional persist (N raw rows + 1 aggregated row), publish
  to an in-memory pub/sub for live gRPC subscribers.
- **Repository** (`internal/repository/`, `pgx/v5`): owns the
  `evm_price` database. Schema in [`migrations/0001_init.up.sql`](migrations/0001_init.up.sql).
- **gRPC** (`internal/grpc/`): implements [`price.v1.PriceService`](https://github.com/asolovov/evm-oracle-demo-protocols/blob/main/price/v1/price.proto)
  — `GetPrice` (cache → repo fall-through) and `Subscribe` (initial
  snapshot + live tail).
- **Healthz** (`internal/healthz/`): `/healthz` (liveness), `/readyz`
  (walks every module's `HealthCheck`), `/metrics` (stub — see
  [Known gaps](#known-gaps)).

---

## Assets covered

| Asset | Class  | CoinGecko id      | Binance pair | Uniswap V3 pool                                | Alpha Vantage | Twelve Data | Stooq    |
|-------|--------|-------------------|--------------|------------------------------------------------|---------------|-------------|----------|
| WETH  | crypto | `weth`            | `ETHUSDT`    | USDC/WETH 0.05%                                 | —             | —           | —        |
| WBTC  | crypto | `wrapped-bitcoin` | `WBTCUSDT`   | USDC/WBTC 0.3%                                  | —             | —           | —        |
| LINK  | crypto | `chainlink`       | `LINKUSDT`   | LINK/WETH 0.3%                                  | —             | —           | —        |
| UNI   | crypto | `uniswap`         | `UNIUSDT`    | UNI/WETH 0.3%                                   | —             | —           | —        |
| AAVE  | crypto | `aave`            | `AAVEUSDT`   | AAVE/WETH 0.3%                                  | —             | —           | —        |
| XAU   | RWA    | —                 | —            | —                                              | `XAU`         | `XAU/USD`   | `xauusd` |
| XAG   | RWA    | —                 | —            | —                                              | `XAG`         | `XAG/USD`   | `xagusd` |
| SPX   | RWA    | —                 | —            | —                                              | `SPX`         | `SPX`       | `^spx`   |
| WTI   | RWA    | —                 | —            | —                                              | `WTI`         | `WTI/USD`   | `cl.f`   |
| HG    | RWA    | —                 | —            | —                                              | `HG`          | `COPPER`    | `hg.f`   |

Crypto refresh: 30 s · RWA refresh: 6 h (per spec NFR-02; free-tier
ceilings make crypto-rate RWA polling impossible).

---

## Quickstart

### 1. Install pinned codegen tools (one-time)

```bash
make tools
```

Installs `buf v1.55.0`, `protoc-gen-go v1.36.0`, `protoc-gen-go-grpc v1.5.1`,
`golang-migrate v4.18.1`, `golangci-lint v1.63.4`.

### 2. Configure secrets

```bash
cp .env.example .env.local
$EDITOR .env.local
```

Fill in the three required API keys — see [`docs/sources.md`](docs/sources.md)
for how to obtain them. `.env.local` is gitignored.

### 3. Run with docker compose

```bash
docker compose --env-file .env.local up --build
```

Compose brings up Postgres, runs the `0001_init` migration to completion,
then starts the service. After ~60 seconds:

```bash
grpcurl -plaintext -d '{"asset_id":"weth"}' localhost:50051 price.v1.PriceService/GetPrice
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

### 4. Run natively (no Docker)

```bash
# 1. Start Postgres yourself (or use compose for just the db)
make migrate-up
make run        # equivalent to `go run -race cmd/server/main.go serve`
```

---

## Configuration

Every config key has a `viper.SetDefault` in [`config/init.go`](config/init.go)
(architecture rule 6). Env vars use `_` as the separator: nested key
`sources.alpha_vantage.api_key` ⇒ env `SOURCES_ALPHA_VANTAGE_API_KEY`.

| Env var                                | Default        | Notes                                              |
|----------------------------------------|----------------|----------------------------------------------------|
| `ENV`                                  | `prod`         | `prod` / `dev` / `local`.                          |
| `DATABASE_HOST`                        | `localhost`    | Postgres host.                                     |
| `DATABASE_PORT`                        | `5432`         |                                                    |
| `DATABASE_USER`                        | `price_user`   | Required.                                          |
| `DATABASE_PASSWORD`                    | *(empty)*      | **Required** — service refuses to start without.   |
| `DATABASE_NAME`                        | `evm_price`    | Architecture rule 7 — service owns this DB.        |
| `GRPC_HOST` / `GRPC_PORT`              | `0.0.0.0:50051`|                                                    |
| `GRPC_REFLECTION`                      | `true`         | Enable for `grpcurl` debugging.                    |
| `HEALTHZ_HOST` / `HEALTHZ_PORT`        | `0.0.0.0:8080` | Serves /healthz, /readyz, /metrics.                |
| `SOURCES_COINGECKO_ENABLED`            | `true`         | No API key needed.                                 |
| `SOURCES_BINANCE_ENABLED`              | `true`         | No API key needed.                                 |
| `SOURCES_STOOQ_ENABLED`                | `true`         | No API key needed.                                 |
| `SOURCES_UNISWAP_V3_API_KEY`           | *(empty)*      | **Required** when uniswap_v3 is enabled.           |
| `SOURCES_ALPHA_VANTAGE_API_KEY`        | *(empty)*      | **Required** when alpha_vantage is enabled.        |
| `SOURCES_TWELVE_DATA_API_KEY`          | *(empty)*      | **Required** when twelve_data is enabled.          |
| `SOURCES_<source>_RATE_LIMIT` / `_BURST` | per-source defaults | Tokens/sec + bucket burst for the source's limiter. |
| `AGGREGATION_MIN_SOURCES`              | `1`            | Min successful fetches required for a round.       |
| `AGGREGATION_MAX_DEVIATION`            | `0.10`         | Reject if `|delta|/last > MaxDeviation`.           |
| `AGGREGATION_FRESHNESS_POLICY`         | `permissive`   | `permissive` (demo) / `strict` (prod semantics).   |
| `AGGREGATION_STALE_AFTER_CRYPTO`       | `300`          | Seconds; only used in strict mode.                 |
| `AGGREGATION_STALE_AFTER_RWA`          | `86400`        | Seconds; only used in strict mode.                 |
| `TELEMETRY_LOG_LEVEL`                  | `info`         | logrus level.                                      |
| `TELEMETRY_LOG_FORMAT`                 | `json`         | `json` / `text`.                                   |

The asset list lives in [`config/init.go`](config/init.go) `defaultAssets()`.
To add or remove an asset, edit that function (operators that need
runtime override can supply a Viper-readable config file with an
overridden `assets:` block).

---

## gRPC surface

Defined upstream in
[`evm-oracle-demo-protocols/price/v1/price.proto`](https://github.com/asolovov/evm-oracle-demo-protocols/blob/main/price/v1/price.proto)
and consumed via the `protocols/` git subtree.

| RPC                | Direction        | Description                                                                 |
|--------------------|------------------|-----------------------------------------------------------------------------|
| `GetPrice`         | Unary            | Most recent `AggregatedPrice` for one asset. `NOT_FOUND` if no rows yet.    |
| `Subscribe`        | Server-streaming | Initial per-asset snapshot, then live tail on every successful tick.        |

Generated Go stubs land in `internal/genproto/` at build time. Per
architecture rule 9 they are **never committed** — see
[`buf.gen.yaml`](buf.gen.yaml) for the codegen config (uses `Mfile=path`
overrides to retarget every proto's `go_package` into this module's
namespace so the protocols subtree stays proto-source-only).

---

## Project layout

```
├── cmd/server/main.go        # cobra/viper entry (architecture rule 1)
├── config/                   # viper defaults + Scheme + Validate
├── internal/
│   ├── application.go        # wires every module (architecture rule 2)
│   ├── models/               # domain types + all conversion methods (rule 3)
│   ├── module/               # generic module-lifecycle framework
│   ├── repository/           # pgx-backed PriceRepository
│   ├── sources/              # 6 adapters + Registry
│   ├── aggregator/           # fan-out, median, deviation, bus
│   ├── grpc/                 # server + handlers (price.v1)
│   ├── healthz/              # /healthz, /readyz, /metrics
│   └── genproto/             # buf-generated proto stubs (gitignored)
├── migrations/               # golang-migrate (0001_init)
├── protocols/                # git subtree of evm-oracle-demo-protocols
├── buf.gen.yaml              # consumer-side codegen config
├── Makefile                  # build / test / generate / migrate
├── Dockerfile                # multi-stage distroless build
└── docker-compose.yml        # Postgres + migrate + service
```

---

## Architectural notes

These rules drive the project's structure. They live in the planning vault
and apply to every Go service in the family:

1. `cmd/` does only Cobra/Viper init.
2. Only `internal/application.go` wires components.
3. Every domain model + conversion method lives in `internal/models/`.
4. Modules: repository, services, servers. Plain packages for cross-service
   gRPC client wrappers.
5. abigen contract bindings live under `pkg/contracts/` (no top-level
   `external/`).
6. All config in `/config`; `viper.SetDefault` is mandatory for every env
   var (`AutomaticEnv` does NOT populate nested keys on `Unmarshal`).
7. One service = one database. price-service owns `evm_price`.
8. Bootstrap data goes through env-var config → `application.Init` →
   service `Bootstrap*` (no seed CLIs, no YAML fixtures, no post-deploy
   jobs).
9. Generated proto / swagger `.go` is **never committed**. Each consumer
   regenerates at build time from pinned codegen tools.

---

## Test coverage

`make test` runs the unit tests; `make test-integration` runs the
Postgres-backed repository round-trip behind a docker daemon.

| Package                     | Coverage | Notes                                                                 |
|-----------------------------|----------|-----------------------------------------------------------------------|
| `internal/aggregator`       | 94.6 %   | Median, deviation guard, freshness policy, fan-out, persist, bus.     |
| `internal/grpc`             | 94.9 %   | Server lifecycle, interceptors (logging + panic recovery).            |
| `internal/grpc/handlers`    | 87.8 %   | `GetPrice` mappings, `Subscribe` snapshot + live tail + filter.       |
| `internal/healthz`          | 92.7 %   | `/healthz` + `/readyz` aggregation behaviour.                         |
| `internal/models`           | 87.5 %   | Enum round-trips, validation, proto<->domain conversions.             |
| `internal/sources`          | 70.8 %   | All six adapters covered via `httptest` (happy + main failure modes). |
| `internal/repository`       | integration | Postgres round-trip via `testcontainers-go` behind `-tags=integration`. |

`make lint` passes clean (golangci-lint v2 schema).

## Known gaps

These are deliberately deferred for the demo scope. Each is straightforward
follow-up work; flagged here so the gap is visible to reviewers.

- **`/metrics` is a stub.** The Prometheus registration scaffolding is in
  place (`telemetry.metrics_enabled` flag, `internal/healthz/`'s
  `/metrics` route) but no counters / histograms are yet wired into the
  aggregator hot path. Adding `price_refresh_total{asset, source, status}`,
  `price_refresh_duration_seconds`, and `price_deviation_ratio` is one
  pass through `internal/aggregator/aggregator.go`.
- **OpenTelemetry traces are not wired.** Spans around source calls and
  aggregation passes are the natural next addition.
- **logrus, not zerolog.** Task spec preferred zerolog with JSON output in
  prod; the template ships logrus, and swapping would have touched every
  log site for marginal benefit on a demo. The
  `TELEMETRY_LOG_FORMAT=json` env knob still produces structured JSON.
- **`docker compose up` smoke test** not executed end-to-end in CI yet.
  The compose file boots Postgres → migrate → service and pins every
  image version; the cold-start happy-path script is left for follow-up.

## Production gaps

The portfolio scope explicitly defers these so the demo stays runnable on
one VPS:

- No HA, no multi-region.
- Source API keys live on disk (`.env.local`); production would use
  Vault / KMS / HSM.
- The deviation guard logs + skips publish on a spike; production should
  page on repeated spikes.
- The aggregator drops sources on `ErrUpstream` without an exponential
  back-off — fine at our cadence, would matter under fan-out load.

---

## Author

**Andrei Solovov** · Senior Blockchain Engineer at
[Gateway.fm](https://gateway.fm).
[GitHub](https://github.com/asolovov) ·
[LinkedIn](https://www.linkedin.com/in/asolovov/)

[evm-oracle-demo]: https://github.com/asolovov
