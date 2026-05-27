package models

import (
	"errors"
	"fmt"
)

// FreshnessPolicy controls how the aggregator treats stale source data.
//
// FreshnessPermissive — demo mode. Source ages are recorded and surfaced on
// the dashboard but never gate the aggregation; every source that returned a
// price contributes to the median regardless of age.
//
// FreshnessStrict — production semantics. Sources older than the per-class
// staleness threshold (see config.AggregationConfig.StaleAfterCrypto /
// StaleAfterRWA) are dropped from the median and tagged included=false.
type FreshnessPolicy int

// FreshnessPolicy enum values. FreshnessUnknown is the zero value and is
// always rejected at boundaries.
const (
	FreshnessUnknown FreshnessPolicy = iota
	FreshnessPermissive
	FreshnessStrict
)

var freshnessStrings = [...]string{
	FreshnessUnknown: unknownLabel,
	FreshnessPermissive: "permissive",
	FreshnessStrict:     "strict",
}

var freshnessByString = map[string]FreshnessPolicy{
	"permissive": FreshnessPermissive,
	"strict":     FreshnessStrict,
}

// String returns the canonical wire representation.
func (p FreshnessPolicy) String() string {
	if p < 0 || int(p) >= len(freshnessStrings) {
		return unknownLabel
	}
	return freshnessStrings[p]
}

// IsValid reports whether p is a known, non-zero FreshnessPolicy.
func (p FreshnessPolicy) IsValid() bool {
	return p > FreshnessUnknown && int(p) < len(freshnessStrings)
}

// FreshnessPolicyFromString parses the canonical wire representation.
func FreshnessPolicyFromString(s string) (FreshnessPolicy, error) {
	if p, ok := freshnessByString[s]; ok {
		return p, nil
	}
	return FreshnessUnknown, fmt.Errorf("%w: %q", ErrUnknownFreshnessPolicy, s)
}

// ErrUnknownFreshnessPolicy is returned when a string cannot be parsed into
// a known FreshnessPolicy.
var ErrUnknownFreshnessPolicy = errors.New("unknown freshness policy")
