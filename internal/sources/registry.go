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

// NewRegistry constructs every enabled adapter from config. Returns
// ErrConfig if a required adapter is enabled without its API key, or if a
// constructor fails.
//
// Disabled adapters are skipped silently — the aggregator copes with a
// reduced source set as long as MinSources is satisfied.
func NewRegistry(cfg config.SourcesConfig) (*Registry, error) {
	r := &Registry{adapters: make(map[models.SourceKind]Adapter, 6)}

	if cfg.CoinGecko.Enabled {
		a, err := NewCoinGecko(cfg.CoinGecko)
		if err != nil {
			return nil, fmt.Errorf("registry: coingecko: %w", err)
		}
		r.adapters[models.SourceCoinGecko] = a
	}
	if cfg.Binance.Enabled {
		a, err := NewBinance(cfg.Binance)
		if err != nil {
			return nil, fmt.Errorf("registry: binance: %w", err)
		}
		r.adapters[models.SourceBinance] = a
	}
	if cfg.UniswapV3.Enabled {
		a, err := NewUniswapV3(cfg.UniswapV3)
		if err != nil {
			return nil, fmt.Errorf("registry: uniswap_v3: %w", err)
		}
		r.adapters[models.SourceUniswapV3] = a
	}
	if cfg.AlphaVantage.Enabled {
		a, err := NewAlphaVantage(cfg.AlphaVantage)
		if err != nil {
			return nil, fmt.Errorf("registry: alpha_vantage: %w", err)
		}
		r.adapters[models.SourceAlphaVantage] = a
	}
	if cfg.TwelveData.Enabled {
		a, err := NewTwelveData(cfg.TwelveData)
		if err != nil {
			return nil, fmt.Errorf("registry: twelve_data: %w", err)
		}
		r.adapters[models.SourceTwelveData] = a
	}
	if cfg.Stooq.Enabled {
		a, err := NewStooq(cfg.Stooq)
		if err != nil {
			return nil, fmt.Errorf("registry: stooq: %w", err)
		}
		r.adapters[models.SourceStooq] = a
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
