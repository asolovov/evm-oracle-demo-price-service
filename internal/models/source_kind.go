package models

import (
	"errors"
	"fmt"
)

// SourceKind enumerates the six price sources this service polls.
//
// Int-backed for cheap comparison + zero-value detection. The string form is
// the stable identifier used everywhere off-chain (config keys, log fields,
// DB column values, the `source` field on proto messages).
type SourceKind int

// SourceKind enum values. SourceUnknown is the zero value and is always
// rejected at boundaries; the remaining values map 1:1 to the adapter
// packages under internal/sources/.
const (
	SourceUnknown SourceKind = iota
	SourceCoinGecko
	SourceBinance
	SourceUniswapV3
	SourceAlphaVantage
	SourceTwelveData
	SourceStooq
)

// sourceKindStrings is the canonical String() representation, indexed by the
// SourceKind value. Order MUST match the iota block above.
var sourceKindStrings = [...]string{
	SourceUnknown:      "unknown",
	SourceCoinGecko:    "coingecko",
	SourceBinance:      "binance",
	SourceUniswapV3:    "uniswap_v3",
	SourceAlphaVantage: "alpha_vantage",
	SourceTwelveData:   "twelve_data",
	SourceStooq:        "stooq",
}

// sourceKindByString is the reverse lookup for SourceKindFromString.
var sourceKindByString = map[string]SourceKind{
	"coingecko":     SourceCoinGecko,
	"binance":       SourceBinance,
	"uniswap_v3":    SourceUniswapV3,
	"alpha_vantage": SourceAlphaVantage,
	"twelve_data":   SourceTwelveData,
	"stooq":         SourceStooq,
}

// AllSources returns every known SourceKind in iteration order. Excludes
// SourceUnknown.
func AllSources() []SourceKind {
	return []SourceKind{
		SourceCoinGecko,
		SourceBinance,
		SourceUniswapV3,
		SourceAlphaVantage,
		SourceTwelveData,
		SourceStooq,
	}
}

// String returns the canonical wire representation.
func (s SourceKind) String() string {
	if s < 0 || int(s) >= len(sourceKindStrings) {
		return "unknown"
	}
	return sourceKindStrings[s]
}

// IsValid reports whether s is a known, non-zero SourceKind.
func (s SourceKind) IsValid() bool {
	return s > SourceUnknown && int(s) < len(sourceKindStrings)
}

// AssetClass returns which class of asset this source covers. Used by the
// aggregator's strict-freshness branch to pick the per-class staleness
// threshold.
func (s SourceKind) AssetClass() AssetClass {
	switch s {
	case SourceCoinGecko, SourceBinance, SourceUniswapV3:
		return AssetClassCrypto
	case SourceAlphaVantage, SourceTwelveData, SourceStooq:
		return AssetClassRWA
	case SourceUnknown:
		return AssetClassUnknown
	default:
		return AssetClassUnknown
	}
}

// SourceKindFromString parses the canonical wire representation. Returns
// SourceUnknown + ErrUnknownSourceKind for unrecognized input.
func SourceKindFromString(s string) (SourceKind, error) {
	if k, ok := sourceKindByString[s]; ok {
		return k, nil
	}
	return SourceUnknown, fmt.Errorf("%w: %q", ErrUnknownSourceKind, s)
}

// ErrUnknownSourceKind is returned when a string cannot be parsed into a
// known SourceKind. Wrapped errors carry the offending input.
var ErrUnknownSourceKind = errors.New("unknown source kind")
