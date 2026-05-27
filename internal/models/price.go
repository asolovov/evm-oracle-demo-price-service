package models

import (
	"errors"
	"time"
)

// RawPrice is the raw observation persisted to `prices_raw` — one source's
// view of one asset at one moment.
//
// Prices flow as IEEE-754 doubles end-to-end in the off-chain pipeline.
// Float→int256 conversion happens once, inside oracle-service, when it builds
// `fulfillPrice` calldata.
type RawPrice struct {
	AssetID          AssetID
	Source           SourceKind
	Price            float64
	FetchedAt        time.Time // when we hit the source
	SourceObservedAt time.Time // upstream-reported timestamp
	// RawPayload is the verbatim upstream response, kept for forensic replay.
	// Adapters serialize to compact JSON before persisting.
	RawPayload []byte
}

// AgeSec returns how stale this price was at the time `now` was sampled,
// measured against SourceObservedAt. Clamped at zero.
func (r RawPrice) AgeSec(now time.Time) int64 {
	if r.SourceObservedAt.IsZero() {
		return 0
	}
	d := now.Sub(r.SourceObservedAt).Seconds()
	if d < 0 {
		return 0
	}
	return int64(d)
}

// SourceContribution is the single-source view of one aggregation round.
// Surfaced on the dashboard's per-source breakdown panel. `Included` reflects
// whether this source contributed to the median (sources may be excluded by
// the freshness or deviation guard in strict mode).
type SourceContribution struct {
	Source           SourceKind
	Price            float64
	FetchedAt        time.Time
	SourceObservedAt time.Time
	AgeSec           int64
	Included         bool
}

// AggregatedPrice is the per-asset, per-tick aggregation output: the median
// across the included source set, with the full per-source breakdown for the
// dashboard. Persisted to `prices_aggregated` and published over the
// in-memory bus for the gRPC `Subscribe` stream.
type AggregatedPrice struct {
	AssetID      AssetID
	MedianPrice  float64
	AggregatedAt time.Time
	WindowStart  time.Time // earliest FetchedAt across included sources
	WindowEnd    time.Time // latest FetchedAt across included sources
	Sources      []SourceContribution
}

// IncludedCount returns how many sources contributed to the median.
func (a AggregatedPrice) IncludedCount() int {
	n := 0
	for _, s := range a.Sources {
		if s.Included {
			n++
		}
	}
	return n
}

// Aggregation-level errors. Wrap to add context.
var (
	// ErrDeviationExceeded is returned when the proposed median deviates from
	// the last accepted price by more than AggregationConfig.MaxDeviation.
	// The aggregator refuses to persist or publish the round.
	ErrDeviationExceeded = errors.New("deviation guard exceeded")

	// ErrNotEnoughSources is returned when fewer than MinSources successful
	// fetches landed for this round. No median is computed.
	ErrNotEnoughSources = errors.New("not enough sources")

	// ErrAssetNotTracked is returned by repository / aggregator lookups when
	// the caller references an asset id that isn't in the configured set.
	ErrAssetNotTracked = errors.New("asset not tracked")
)
