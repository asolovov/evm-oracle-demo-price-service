package sources

import (
	"fmt"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Registry maps a SourceKind to its constructed Adapter. The aggregator
// iterates over an asset's source set and calls Registry.Get for each.
type Registry struct {
	adapters map[models.SourceKind]Adapter
}

// adapterSpec ties a config block to the SourceKind it registers and the
// constructor that builds it. NewRegistry walks this table so adding a
// source is one row, not another branch.
type adapterSpec struct {
	kind models.SourceKind
	cfg  config.SourceConfig
	ctor func(config.SourceConfig) (Adapter, error)
}

// NewRegistry constructs every enabled adapter from config. Returns
// ErrConfig if a required adapter is enabled without its API key, or if a
// constructor fails.
//
// Disabled adapters are skipped silently — the aggregator copes with a
// reduced source set as long as MinSources is satisfied.
func NewRegistry(cfg config.SourcesConfig) (*Registry, error) {
	specs := []adapterSpec{
		{models.SourceCoinGecko, cfg.CoinGecko, NewCoinGecko},
		{models.SourceBinance, cfg.Binance, NewBinance},
		{models.SourceUniswapV3, cfg.UniswapV3, NewUniswapV3},
		{models.SourceAlphaVantage, cfg.AlphaVantage, NewAlphaVantage},
		{models.SourceTwelveData, cfg.TwelveData, NewTwelveData},
		{models.SourceStooq, cfg.Stooq, NewStooq},
		{models.SourceGoldAPI, cfg.GoldAPI, NewGoldAPI},
		{models.SourceYahoo, cfg.Yahoo, NewYahoo},
		{models.SourceEIA, cfg.EIA, NewEIA},
		{models.SourceFRED, cfg.FRED, NewFRED},
		{models.SourceSwissquote, cfg.Swissquote, NewSwissquote},
	}

	r := &Registry{adapters: make(map[models.SourceKind]Adapter, len(specs))}
	for _, s := range specs {
		if !s.cfg.Enabled {
			continue
		}
		a, err := s.ctor(s.cfg)
		if err != nil {
			return nil, fmt.Errorf("registry: %s: %w", s.kind, err)
		}
		r.adapters[s.kind] = a
	}

	if len(r.adapters) == 0 {
		return nil, fmt.Errorf("%w: no source adapters enabled", ErrConfig)
	}
	return r, nil
}

// Get returns the adapter for the given source kind. Returns (nil, false)
// when the adapter is disabled or unknown.
func (r *Registry) Get(k models.SourceKind) (Adapter, bool) {
	a, ok := r.adapters[k]
	return a, ok
}

// All returns every registered adapter (iteration order undefined).
func (r *Registry) All() map[models.SourceKind]Adapter {
	out := make(map[models.SourceKind]Adapter, len(r.adapters))
	for k, v := range r.adapters {
		out[k] = v
	}
	return out
}
