// Package config defines application configuration types and defaults.
package config

import (
	"errors"
	"fmt"

	"github.com/spf13/viper"
)

// init registers viper defaults at package load.
//
//nolint:gochecknoinits // configuration defaults are registered at package load.
func init() {
	setDefaults()
}

// setDefaults exposes default registration for testing.
//
// Architecture rule 6 — `viper.SetDefault` is mandatory for every env var the
// service reads. `viper.AutomaticEnv()` alone does NOT populate nested keys on
// `Unmarshal`; the key must be registered here. Use "" / 0 / false for keys
// the operator MUST supply (Validate fails fast on those).
func setDefaults() {
	// Core
	viper.SetDefault("env", "prod")

	// Database — service owns `evm_price` per architecture rule 7.
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "price_user")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.name", "evm_price")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_open_conns", 25)
	viper.SetDefault("database.max_idle_conns", 5)
	viper.SetDefault("database.conn_max_lifetime", 300)

	// gRPC server
	viper.SetDefault("grpc.host", "0.0.0.0")
	viper.SetDefault("grpc.port", 50051)
	viper.SetDefault("grpc.timeout", "30s")
	viper.SetDefault("grpc.max_send_msg_size", 16*1024*1024)
	viper.SetDefault("grpc.max_recv_msg_size", 16*1024*1024)
	viper.SetDefault("grpc.num_stream_workers", 0)
	viper.SetDefault("grpc.reflection", true)

	// Healthz / metrics HTTP
	viper.SetDefault("healthz.host", "0.0.0.0")
	viper.SetDefault("healthz.port", 8080)

	// Sources — defaults reflect free-tier endpoints and conservative rate limits.
	// Operators override API keys via env (e.g. SOURCES_ALPHA_VANTAGE_API_KEY).

	viper.SetDefault("sources.coingecko.enabled", true)
	viper.SetDefault("sources.coingecko.base_url", "https://api.coingecko.com")
	viper.SetDefault("sources.coingecko.api_key", "")
	viper.SetDefault("sources.coingecko.timeout", "10s")
	viper.SetDefault("sources.coingecko.rate_limit", 0.4) // ~24 req/min free tier
	viper.SetDefault("sources.coingecko.burst", 2)

	viper.SetDefault("sources.binance.enabled", true)
	viper.SetDefault("sources.binance.base_url", "https://api.binance.com")
	viper.SetDefault("sources.binance.api_key", "")
	viper.SetDefault("sources.binance.timeout", "10s")
	viper.SetDefault("sources.binance.rate_limit", 5.0)
	viper.SetDefault("sources.binance.burst", 10)

	viper.SetDefault("sources.uniswap_v3.enabled", true)
	viper.SetDefault("sources.uniswap_v3.base_url", "https://gateway.thegraph.com/api")
	viper.SetDefault("sources.uniswap_v3.api_key", "")
	viper.SetDefault("sources.uniswap_v3.timeout", "15s")
	viper.SetDefault("sources.uniswap_v3.rate_limit", 1.0)
	viper.SetDefault("sources.uniswap_v3.burst", 3)

	viper.SetDefault("sources.alpha_vantage.enabled", true)
	viper.SetDefault("sources.alpha_vantage.base_url", "https://www.alphavantage.co")
	viper.SetDefault("sources.alpha_vantage.api_key", "")
	viper.SetDefault("sources.alpha_vantage.timeout", "15s")
	viper.SetDefault("sources.alpha_vantage.rate_limit", 0.08) // ~5 req/min free
	viper.SetDefault("sources.alpha_vantage.burst", 1)

	// Twelve Data: DISABLED by default. Its free tier paywalls every RWA
	// symbol we need (XAG/USD, WTI/USD, COPPER, SPX → 404 "Grow/Venture
	// plan"), so it serves nothing here. Left in config so it can be
	// re-enabled with a paid key. Disabled => its api_key is not required
	// at startup (see Validate) and no adapter is built.
	viper.SetDefault("sources.twelve_data.enabled", false)
	viper.SetDefault("sources.twelve_data.base_url", "https://api.twelvedata.com")
	viper.SetDefault("sources.twelve_data.api_key", "")
	viper.SetDefault("sources.twelve_data.timeout", "15s")
	viper.SetDefault("sources.twelve_data.rate_limit", 0.13) // ~8 req/min free
	viper.SetDefault("sources.twelve_data.burst", 2)

	// Stooq: DISABLED by default. The CSV quote endpoint
	// (stooq.com/q/l/?...&e=csv) returns an HTML error page for every
	// symbol (bot/geo block), so it never produces data. Left in config
	// in case the endpoint becomes reachable again.
	viper.SetDefault("sources.stooq.enabled", false)
	viper.SetDefault("sources.stooq.base_url", "https://stooq.com")
	viper.SetDefault("sources.stooq.api_key", "")
	viper.SetDefault("sources.stooq.timeout", "15s")
	viper.SetDefault("sources.stooq.rate_limit", 1.0)
	viper.SetDefault("sources.stooq.burst", 2)

	// gold-api.com — no auth, no documented rate limit. Covers XAU/XAG/HG.
	// NOTE: api.gold-api.com is a different service from goldapi.io.
	viper.SetDefault("sources.gold_api.enabled", true)
	viper.SetDefault("sources.gold_api.base_url", "https://api.gold-api.com")
	viper.SetDefault("sources.gold_api.api_key", "")
	viper.SetDefault("sources.gold_api.timeout", "10s")
	viper.SetDefault("sources.gold_api.rate_limit", 1.0)
	viper.SetDefault("sources.gold_api.burst", 2)

	// Yahoo Finance v8 chart — no auth. Covers all RWA (GC=F/SI=F/CL=F/
	// HG=F/^GSPC). Unofficial endpoint; community guidance ~0.5s spacing.
	viper.SetDefault("sources.yahoo.enabled", true)
	viper.SetDefault("sources.yahoo.base_url", "https://query1.finance.yahoo.com")
	viper.SetDefault("sources.yahoo.api_key", "")
	viper.SetDefault("sources.yahoo.timeout", "10s")
	viper.SetDefault("sources.yahoo.rate_limit", 2.0)
	viper.SetDefault("sources.yahoo.burst", 4)

	// EIA Open Data v2 — free key required. WTI daily spot (RWTC).
	// ~9000 req/hr, so generous limits; daily cadence + multi-day lag.
	viper.SetDefault("sources.eia.enabled", true)
	viper.SetDefault("sources.eia.base_url", "https://api.eia.gov")
	viper.SetDefault("sources.eia.api_key", "")
	viper.SetDefault("sources.eia.timeout", "15s")
	viper.SetDefault("sources.eia.rate_limit", 5.0)
	viper.SetDefault("sources.eia.burst", 5)

	// FRED (St. Louis Fed) — free key required. WTI (DCOILWTICO) + SPX
	// (SP500), both daily close / business-day lag. 120 req/min.
	viper.SetDefault("sources.fred.enabled", true)
	viper.SetDefault("sources.fred.base_url", "https://api.stlouisfed.org")
	viper.SetDefault("sources.fred.api_key", "")
	viper.SetDefault("sources.fred.timeout", "15s")
	viper.SetDefault("sources.fred.rate_limit", 2.0)
	viper.SetDefault("sources.fred.burst", 4)

	// Swissquote public feed — no auth. Metals (XAU/XAG). Unofficial.
	viper.SetDefault("sources.swissquote.enabled", true)
	viper.SetDefault("sources.swissquote.base_url", "https://forex-data-feed.swissquote.com")
	viper.SetDefault("sources.swissquote.api_key", "")
	viper.SetDefault("sources.swissquote.timeout", "10s")
	viper.SetDefault("sources.swissquote.rate_limit", 1.0)
	viper.SetDefault("sources.swissquote.burst", 2)

	// Aggregation — demo-permissive freshness by default (spec §5.3).
	viper.SetDefault("aggregation.min_sources", 1)
	viper.SetDefault("aggregation.max_deviation", 0.10)
	viper.SetDefault("aggregation.freshness_policy", "permissive")
	viper.SetDefault("aggregation.stale_after_crypto", 300)    // 5 min
	viper.SetDefault("aggregation.stale_after_rwa", 24*60*60)  // 24 h

	// Assets — the 10 tracked assets and which sources cover which symbol.
	// Operators override the full list via a config file when needed; env-var
	// override of a list is intentionally not supported (see docs/sources.md).
	viper.SetDefault("assets", defaultAssets())

	// Telemetry
	viper.SetDefault("telemetry.log_level", "info")
	viper.SetDefault("telemetry.log_format", "json")
	viper.SetDefault("telemetry.metrics_enabled", true)
	viper.SetDefault("telemetry.otlp_endpoint", "")
}

// defaultAssets returns the 10 tracked assets with per-source symbol mappings.
//
// The Symbols map keys must match SourceKind.String() values
// ("coingecko", "binance", "uniswap_v3", "alpha_vantage", "twelve_data", "stooq").
// For Uniswap V3 the value is the pool address (a 0x-prefixed hex string); the
// adapter resolves token0/token1 from the pool.
func defaultAssets() []map[string]interface{} {
	return []map[string]interface{}{
		// --- Crypto (refresh every 180s) ---
		// 5 crypto assets × 86400/180 = 2400 calls/day per source. Sized to
		// fit the Graph Gateway 100 K/month (~3 333/day) free tier with
		// headroom; CoinGecko and Binance free tiers are well above this.
		{
			"id":    "weth",
			"class": "crypto",
			"symbols": map[string]string{
				"coingecko":  "weth",
				"binance":    "ETHUSDT",
				"uniswap_v3": "0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640", // USDC/WETH 0.05%
			},
			"refresh_interval_sec": 180,
		},
		{
			"id":    "wbtc",
			"class": "crypto",
			"symbols": map[string]string{
				"coingecko":  "wrapped-bitcoin",
				"binance":    "WBTCUSDT",
				"uniswap_v3": "0x99ac8ca7087fa4a2a1fb6357269965a2014abc35", // USDC/WBTC 0.3%
			},
			"refresh_interval_sec": 180,
		},
		{
			"id":    "link",
			"class": "crypto",
			"symbols": map[string]string{
				"coingecko":  "chainlink",
				"binance":    "LINKUSDT",
				"uniswap_v3": "0xfad57d2039c21811c8f2b5d5b65308aa99d31559", // LINK/WETH 0.3%
			},
			"refresh_interval_sec": 180,
		},
		{
			"id":    "uni",
			"class": "crypto",
			"symbols": map[string]string{
				"coingecko":  "uniswap",
				"binance":    "UNIUSDT",
				"uniswap_v3": "0x1d42064fc4beb5f8aaf85f4617ae8b3b5b8bd801", // UNI/WETH 0.3%
			},
			"refresh_interval_sec": 180,
		},
		{
			"id":    "aave",
			"class": "crypto",
			"symbols": map[string]string{
				"coingecko":  "aave",
				"binance":    "AAVEUSDT",
				"uniswap_v3": "0x5ab53ee1d50eef2c1dd3d5402789cd27bb52c1bb", // AAVE/WETH 0.3%
			},
			"refresh_interval_sec": 180,
		},
		// --- RWA (refresh every 12h) ---
		// Source set reworked in task 05.2 (the original alpha_vantage /
		// twelve_data / stooq mix was broken — AV GLOBAL_QUOTE returned
		// equities for SPX/WTI/HG, TD free-tier paywalls RWA, Stooq CSV is
		// dead). See docs/sources.md and the rwa-data-source-research note.
		//
		// Cadence note: metals + copper use real-time sources. WTI uses EIA +
		// FRED (both daily official-gov spot, mutually consistent — avoids
		// mixing a real-time future with a daily spot). SPX mixes Yahoo
		// (real-time) + FRED (daily close) because no 2nd real-time free SPX
		// source exists; the median may lag intraday. Symbols:
		//   gold_api: XAU / XAG / HG       yahoo: GC=F / SI=F / HG=F / CL=F / ^GSPC
		//   eia:      RWTC (WTI series)    fred:  DCOILWTICO (WTI) / SP500 (SPX)
		//   alpha_vantage: XAU / XAG (FX)  swissquote: XAU / XAG
		{
			"id":    "xau",
			"class": "rwa",
			"symbols": map[string]string{
				"gold_api":      "XAU",
				"yahoo":         "GC=F",
				"alpha_vantage": "XAU",
				"swissquote":    "XAU",
			},
			"refresh_interval_sec": 12 * 60 * 60,
		},
		{
			"id":    "xag",
			"class": "rwa",
			"symbols": map[string]string{
				"gold_api":      "XAG",
				"yahoo":         "SI=F",
				"alpha_vantage": "XAG",
				"swissquote":    "XAG",
			},
			"refresh_interval_sec": 12 * 60 * 60,
		},
		{
			"id":    "spx",
			"class": "rwa",
			"symbols": map[string]string{
				"yahoo": "^GSPC",
				"fred":  "SP500",
			},
			"refresh_interval_sec": 12 * 60 * 60,
		},
		{
			"id":    "wti",
			"class": "rwa",
			"symbols": map[string]string{
				"eia":  "RWTC",
				"fred": "DCOILWTICO",
			},
			"refresh_interval_sec": 12 * 60 * 60,
		},
		{
			"id":    "hg",
			"class": "rwa",
			"symbols": map[string]string{
				"gold_api": "HG",
				"yahoo":    "HG=F",
			},
			"refresh_interval_sec": 12 * 60 * 60,
		},
	}
}

// Validate fails fast on configuration that would crash-loop the service
// later. Required keys are deliberately defaulted to "" / 0 / "permissive"
// above so the operator's misconfiguration surfaces here instead of inside
// the adapter or repository layer.
func (s *Scheme) Validate() error {
	if s.Database.User == "" {
		return errors.New("database.user is required")
	}
	if s.Database.Password == "" {
		return errors.New("database.password is required")
	}
	if s.Database.Name == "" {
		return errors.New("database.name is required")
	}
	if s.GRPC.Port == 0 {
		return errors.New("grpc.port is required")
	}
	if s.Aggregation.MinSources < 1 {
		return fmt.Errorf("aggregation.min_sources must be >= 1, got %d", s.Aggregation.MinSources)
	}
	if s.Aggregation.MaxDeviation <= 0 || s.Aggregation.MaxDeviation > 1 {
		return fmt.Errorf("aggregation.max_deviation must be in (0, 1], got %v", s.Aggregation.MaxDeviation)
	}
	if s.Aggregation.FreshnessPolicy != "permissive" && s.Aggregation.FreshnessPolicy != "strict" {
		return fmt.Errorf("aggregation.freshness_policy must be 'permissive' or 'strict', got %q", s.Aggregation.FreshnessPolicy)
	}
	if len(s.Assets) == 0 {
		return errors.New("at least one asset must be configured")
	}
	for i, a := range s.Assets {
		if a.ID == "" {
			return fmt.Errorf("assets[%d].id is required", i)
		}
		if a.Class != "crypto" && a.Class != "rwa" {
			return fmt.Errorf("assets[%d].class must be 'crypto' or 'rwa', got %q", i, a.Class)
		}
		if len(a.Symbols) == 0 {
			return fmt.Errorf("assets[%d] (%s): at least one source symbol is required", i, a.ID)
		}
		if a.RefreshIntervalSec <= 0 {
			return fmt.Errorf("assets[%d] (%s): refresh_interval_sec must be > 0", i, a.ID)
		}
	}
	// Sources whose API key is empty but whose adapter requires one are flagged.
	return s.validateSourceKeys()
}

// validateSourceKeys fails fast when a key-requiring source is enabled
// without its API key. Keyless sources (CoinGecko, Binance, Stooq, gold-api,
// Yahoo, Swissquote) are not listed.
func (s *Scheme) validateSourceKeys() error {
	keyed := []struct {
		src  SourceConfig
		name string
		hint string
	}{
		{s.Sources.UniswapV3, "uniswap_v3", ""},
		{s.Sources.AlphaVantage, "alpha_vantage", ""},
		{s.Sources.TwelveData, "twelve_data", ""},
		{s.Sources.EIA, "eia", " (free key: https://www.eia.gov/opendata/register.php)"},
		{s.Sources.FRED, "fred", " (free key: https://fred.stlouisfed.org/docs/api/api_key.html)"},
	}
	for _, k := range keyed {
		if k.src.Enabled && k.src.APIKey == "" {
			return fmt.Errorf("sources.%s.api_key is required when the %s source is enabled%s", k.name, k.name, k.hint)
		}
	}
	return nil
}
