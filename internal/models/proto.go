package models

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pricev1 "github.com/asolovov/evm-oracle-demo-price-service/internal/genproto/price/v1"
)

// Proto conversions live here per architecture rule 3 — every model<->wire
// conversion is a method on the domain type.

// ToProto returns the proto AggregatedPrice that wraps this domain value.
// Sources are emitted in their stored order; the dashboard re-orders by
// SourceKind for display.
func (a AggregatedPrice) ToProto() *pricev1.AggregatedPrice {
	contributions := make([]*pricev1.SourceContribution, 0, len(a.Sources))
	for _, c := range a.Sources {
		contributions = append(contributions, c.ToProto())
	}
	return &pricev1.AggregatedPrice{
		AssetId:      string(a.AssetID),
		MedianPrice:  a.MedianPrice,
		AggregatedAt: tsOrNil(a.AggregatedAt),
		Sources:      contributions,
	}
}

// AggregatedPriceFromProto parses a wire-format AggregatedPrice. Returns
// ErrInvalidAssetID when the asset id is empty / malformed.
func AggregatedPriceFromProto(p *pricev1.AggregatedPrice) (AggregatedPrice, error) {
	id := AssetID(p.GetAssetId())
	if err := id.Validate(); err != nil {
		return AggregatedPrice{}, err
	}
	sources := make([]SourceContribution, 0, len(p.GetSources()))
	for _, c := range p.GetSources() {
		sources = append(sources, SourceContributionFromProto(c))
	}
	return AggregatedPrice{
		AssetID:      id,
		MedianPrice:  p.GetMedianPrice(),
		AggregatedAt: tsOrZero(p.GetAggregatedAt()),
		Sources:      sources,
	}, nil
}

// ToProto returns the proto SourceContribution for one row.
func (c SourceContribution) ToProto() *pricev1.SourceContribution {
	return &pricev1.SourceContribution{
		Source:           c.Source.String(),
		Price:            c.Price,
		FetchedAt:        tsOrNil(c.FetchedAt),
		SourceObservedAt: tsOrNil(c.SourceObservedAt),
		AgeSec:           c.AgeSec,
		Included:         c.Included,
	}
}

// SourceContributionFromProto parses a proto SourceContribution. Unknown
// source strings degrade to SourceUnknown (forward-compatible).
func SourceContributionFromProto(c *pricev1.SourceContribution) SourceContribution {
	src, err := SourceKindFromString(c.GetSource())
	if err != nil {
		src = SourceUnknown
	}
	return SourceContribution{
		Source:           src,
		Price:            c.GetPrice(),
		FetchedAt:        tsOrZero(c.GetFetchedAt()),
		SourceObservedAt: tsOrZero(c.GetSourceObservedAt()),
		AgeSec:           c.GetAgeSec(),
		Included:         c.GetIncluded(),
	}
}

func tsOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tsOrZero(p *timestamppb.Timestamp) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.AsTime()
}
