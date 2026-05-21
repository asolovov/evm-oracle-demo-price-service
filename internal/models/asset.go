// Package models holds the domain types for the price-service plus every
// conversion method between them (proto, DB, string forms). Per
// architecture rule 3, parsing/conversion always lives on the model type.
package models

import (
	"errors"
	"fmt"
	"strings"
)

// AssetID is the canonical identifier for a tracked asset. Lowercase,
// matches the on-chain bytes32 key. Operators may add new assets via config
// without recompiling, so this is a string type rather than a closed enum.
type AssetID string

// String implements fmt.Stringer for log + DB use.
func (a AssetID) String() string {
	return string(a)
}

// Validate enforces shape constraints (non-empty, lowercase, no whitespace).
// Returns ErrInvalidAssetID wrapped with the offending input.
func (a AssetID) Validate() error {
	if a == "" {
		return fmt.Errorf("%w: empty", ErrInvalidAssetID)
	}
	s := string(a)
	if s != strings.ToLower(s) {
		return fmt.Errorf("%w: must be lowercase, got %q", ErrInvalidAssetID, s)
	}
	if strings.ContainsAny(s, " \t\n\r") {
		return fmt.Errorf("%w: contains whitespace: %q", ErrInvalidAssetID, s)
	}
	return nil
}

// Well-known asset IDs. These are the ten assets the demo covers out of the
// box, exposed as typed constants for code that wants to refer to a specific
// asset (tests, defaults, log fields). Operators may configure additional
// IDs at runtime.
const (
	AssetWETH AssetID = "weth"
	AssetWBTC AssetID = "wbtc"
	AssetLINK AssetID = "link"
	AssetUNI  AssetID = "uni"
	AssetAAVE AssetID = "aave"
	AssetXAU  AssetID = "xau"
	AssetXAG  AssetID = "xag"
	AssetSPX  AssetID = "spx"
	AssetWTI  AssetID = "wti"
	AssetHG   AssetID = "hg"
)

// AssetClass discriminates crypto vs RWA. Used by the aggregator's
// strict-freshness path to pick which staleness threshold applies.
type AssetClass int

// AssetClass enum values. AssetClassUnknown is the zero value and is
// always rejected at boundaries.
const (
	AssetClassUnknown AssetClass = iota
	AssetClassCrypto
	AssetClassRWA
)

var assetClassStrings = [...]string{
	AssetClassUnknown: "unknown",
	AssetClassCrypto:  "crypto",
	AssetClassRWA:     "rwa",
}

var assetClassByString = map[string]AssetClass{
	"crypto": AssetClassCrypto,
	"rwa":    AssetClassRWA,
}

// String returns the canonical wire representation.
func (c AssetClass) String() string {
	if c < 0 || int(c) >= len(assetClassStrings) {
		return "unknown"
	}
	return assetClassStrings[c]
}

// IsValid reports whether c is a known, non-zero AssetClass.
func (c AssetClass) IsValid() bool {
	return c > AssetClassUnknown && int(c) < len(assetClassStrings)
}

// AssetClassFromString parses the canonical wire representation.
func AssetClassFromString(s string) (AssetClass, error) {
	if c, ok := assetClassByString[s]; ok {
		return c, nil
	}
	return AssetClassUnknown, fmt.Errorf("%w: %q", ErrInvalidAssetClass, s)
}

// Asset is the runtime descriptor for one tracked asset — its ID, class,
// per-source symbol mapping, and refresh cadence.
//
// Constructed in `application.go` from config.AssetConfig (using NewAsset),
// and passed down to the aggregator + source adapters. Never mutated after
// construction.
type Asset struct {
	ID                 AssetID
	Class              AssetClass
	Symbols            map[SourceKind]string // adapter symbol per source
	RefreshIntervalSec int
}

// NewAsset builds an Asset from the config-shaped inputs. Validates the ID,
// parses class + source-key strings, and rejects empty / unknown values.
func NewAsset(id, class string, symbols map[string]string, refreshIntervalSec int) (Asset, error) {
	aid := AssetID(id)
	if err := aid.Validate(); err != nil {
		return Asset{}, err
	}

	cls, err := AssetClassFromString(class)
	if err != nil {
		return Asset{}, fmt.Errorf("asset %q class: %w", id, err)
	}

	if refreshIntervalSec <= 0 {
		return Asset{}, fmt.Errorf("%w: asset %q refresh_interval_sec must be > 0, got %d",
			ErrInvalidAsset, id, refreshIntervalSec)
	}

	if len(symbols) == 0 {
		return Asset{}, fmt.Errorf("%w: asset %q has no source symbols", ErrInvalidAsset, id)
	}

	parsed := make(map[SourceKind]string, len(symbols))
	for srcStr, sym := range symbols {
		src, err := SourceKindFromString(srcStr)
		if err != nil {
			return Asset{}, fmt.Errorf("asset %q symbols: %w", id, err)
		}
		if sym == "" {
			return Asset{}, fmt.Errorf("%w: asset %q has empty symbol for source %q",
				ErrInvalidAsset, id, srcStr)
		}
		// Reject crypto-class assets pointing at RWA sources and vice versa —
		// the aggregator does not mix classes.
		if src.AssetClass() != cls {
			return Asset{}, fmt.Errorf("%w: asset %q (%s) references source %q (%s)",
				ErrInvalidAsset, id, cls, srcStr, src.AssetClass())
		}
		parsed[src] = sym
	}

	return Asset{
		ID:                 aid,
		Class:              cls,
		Symbols:            parsed,
		RefreshIntervalSec: refreshIntervalSec,
	}, nil
}

// Sources returns the source kinds that cover this asset, in iteration order
// (map iteration; callers that need determinism should sort).
func (a Asset) Sources() []SourceKind {
	out := make([]SourceKind, 0, len(a.Symbols))
	for s := range a.Symbols {
		out = append(out, s)
	}
	return out
}

// SymbolFor returns the source-specific symbol for this asset and a bool
// indicating whether the source covers the asset.
func (a Asset) SymbolFor(s SourceKind) (string, bool) {
	sym, ok := a.Symbols[s]
	return sym, ok
}

// Errors raised by the asset model. Wrap to add context; do not redefine.
var (
	ErrInvalidAssetID    = errors.New("invalid asset id")
	ErrInvalidAssetClass = errors.New("invalid asset class")
	ErrInvalidAsset      = errors.New("invalid asset")
)
