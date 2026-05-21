// Package config defines application configuration types.
//
// Per architecture rule 6, every configuration concern lives in this package:
// defaults, validation, and the Scheme struct that the rest of the service
// reads from. Nothing outside /config registers viper defaults or parses env
// vars — the rest of the service receives a fully-populated *Scheme.
package config

// Scheme is the root configuration for the price-service.
//
// Fields are concrete (no Enabled toggles) because every block is required —
// the price-service has no optional modules. The shape matches the task spec:
// database, gRPC, healthz, sources, assets, aggregation, telemetry.
type Scheme struct {
	Env         string            `mapstructure:"env"`
	Database    DatabaseConfig    `mapstructure:"database"`
	GRPC        GRPCConfig        `mapstructure:"grpc"`
	Healthz     HealthzConfig     `mapstructure:"healthz"`
	Sources     SourcesConfig     `mapstructure:"sources"`
	Aggregation AggregationConfig `mapstructure:"aggregation"`
	Assets      []AssetConfig     `mapstructure:"assets"`
	Telemetry   TelemetryConfig   `mapstructure:"telemetry"`
}

// DatabaseConfig holds PostgreSQL connection settings. The price-service owns
// the dedicated database `evm_price` per architecture rule 7.
type DatabaseConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	Name            string `mapstructure:"name"`
	SSLMode         string `mapstructure:"ssl_mode"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"` // seconds
}

// GRPCConfig holds gRPC server settings. The service is gRPC-only externally —
// healthz HTTP lives in HealthzConfig below.
type GRPCConfig struct {
	Host             string `mapstructure:"host"`
	Port             int    `mapstructure:"port"`
	Timeout          string `mapstructure:"timeout"`
	MaxSendMsgSize   int    `mapstructure:"max_send_msg_size"`
	MaxRecvMsgSize   int    `mapstructure:"max_recv_msg_size"`
	NumStreamWorkers uint32 `mapstructure:"num_stream_workers"`
	Reflection       bool   `mapstructure:"reflection"`
}

// HealthzConfig holds the lightweight HTTP listener that exposes /healthz,
// /readyz, and /metrics. Distinct from a full HTTP API surface — the
// price-service does not speak HTTP application traffic.
type HealthzConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// SourcesConfig holds per-adapter settings for the six price sources.
type SourcesConfig struct {
	CoinGecko    SourceConfig `mapstructure:"coingecko"`
	Binance      SourceConfig `mapstructure:"binance"`
	UniswapV3    SourceConfig `mapstructure:"uniswap_v3"`
	AlphaVantage SourceConfig `mapstructure:"alpha_vantage"`
	TwelveData   SourceConfig `mapstructure:"twelve_data"`
	Stooq        SourceConfig `mapstructure:"stooq"`
}

// SourceConfig is the uniform shape for every source adapter.
//
// RateLimit is tokens-per-second for an internal token bucket; Burst is the
// bucket capacity. APIKey is empty for sources that don't require auth
// (CoinGecko, Binance public, Stooq).
type SourceConfig struct {
	Enabled   bool    `mapstructure:"enabled"`
	BaseURL   string  `mapstructure:"base_url"`
	APIKey    string  `mapstructure:"api_key"`
	Timeout   string  `mapstructure:"timeout"`
	RateLimit float64 `mapstructure:"rate_limit"`
	Burst     int     `mapstructure:"burst"`
}

// AggregationConfig drives the per-tick aggregation pass.
//
// FreshnessPolicy is "permissive" (demo default — ages flow through, never
// gate) or "strict" (production semantics — sources older than the per-class
// threshold are dropped from the median). See spec §5.3.
type AggregationConfig struct {
	MinSources       int     `mapstructure:"min_sources"`
	MaxDeviation     float64 `mapstructure:"max_deviation"`      // 0.10 = 10%
	FreshnessPolicy  string  `mapstructure:"freshness_policy"`   // "permissive" | "strict"
	StaleAfterCrypto int     `mapstructure:"stale_after_crypto"` // seconds
	StaleAfterRWA    int     `mapstructure:"stale_after_rwa"`    // seconds
}

// AssetConfig describes one tracked asset across all configured sources.
//
// Symbols maps the SourceKind string ("coingecko", "binance", ...) to the
// source-specific symbol used when querying that source. Sources whose name
// is absent from this map don't track this asset.
type AssetConfig struct {
	ID                 string            `mapstructure:"id"`
	Class              string            `mapstructure:"class"` // "crypto" | "rwa"
	Symbols            map[string]string `mapstructure:"symbols"`
	RefreshIntervalSec int               `mapstructure:"refresh_interval_sec"`
}

// TelemetryConfig holds structured logging + metrics + tracing settings.
type TelemetryConfig struct {
	LogLevel       string `mapstructure:"log_level"`
	LogFormat      string `mapstructure:"log_format"`      // "json" | "text"
	MetricsEnabled bool   `mapstructure:"metrics_enabled"` // expose /metrics
	OTLPEndpoint   string `mapstructure:"otlp_endpoint"`   // empty disables OTel
}
