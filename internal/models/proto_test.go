package models

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pricev1 "github.com/asolovov/evm-oracle-demo-price-service/internal/genproto/price/v1"
)

func TestAggregatedPriceProtoRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	orig := AggregatedPrice{
		AssetID:      "weth",
		MedianPrice:  3450.12,
		AggregatedAt: t0,
		Sources: []SourceContribution{
			{
				Source:           SourceCoinGecko,
				Price:            3450,
				FetchedAt:        t0,
				SourceObservedAt: t0,
				AgeSec:           10,
				Included:         true,
			},
			{
				Source:           SourceBinance,
				Price:            3451,
				FetchedAt:        t0.Add(2 * time.Second),
				SourceObservedAt: t0.Add(2 * time.Second),
				AgeSec:           12,
				Included:         true,
			},
		},
	}

	p := orig.ToProto()
	if p.GetAssetId() != "weth" {
		t.Fatalf("AssetId = %q", p.GetAssetId())
	}
	if p.GetMedianPrice() != 3450.12 {
		t.Fatalf("MedianPrice = %v", p.GetMedianPrice())
	}
	if len(p.GetSources()) != 2 {
		t.Fatalf("Sources len = %d, want 2", len(p.GetSources()))
	}

	back, err := AggregatedPriceFromProto(p)
	if err != nil {
		t.Fatalf("AggregatedPriceFromProto: %v", err)
	}
	if back.AssetID != orig.AssetID {
		t.Fatalf("AssetID round-trip: %v != %v", back.AssetID, orig.AssetID)
	}
	if back.MedianPrice != orig.MedianPrice {
		t.Fatalf("MedianPrice round-trip: %v != %v", back.MedianPrice, orig.MedianPrice)
	}
	if !back.AggregatedAt.Equal(orig.AggregatedAt) {
		t.Fatalf("AggregatedAt round-trip: %v != %v", back.AggregatedAt, orig.AggregatedAt)
	}
	if len(back.Sources) != 2 {
		t.Fatalf("Sources len round-trip: %d", len(back.Sources))
	}
	if back.Sources[0].Source != SourceCoinGecko {
		t.Fatalf("Source[0] = %v", back.Sources[0].Source)
	}
}

func TestAggregatedPriceFromProtoRejectsInvalidAssetID(t *testing.T) {
	_, err := AggregatedPriceFromProto(&pricev1.AggregatedPrice{AssetId: "WETH"})
	if err == nil {
		t.Fatalf("expected validation error for uppercase asset id")
	}
}

func TestSourceContributionUnknownSourceDegrades(t *testing.T) {
	c := SourceContributionFromProto(&pricev1.SourceContribution{
		Source:           "not-a-source",
		Price:            123.45,
		FetchedAt:        timestamppb.New(time.Now().UTC()),
		SourceObservedAt: timestamppb.New(time.Now().UTC()),
		AgeSec:           7,
		Included:         true,
	})
	if c.Source != SourceUnknown {
		t.Fatalf("expected SourceUnknown for unrecognised source, got %v", c.Source)
	}
	if c.Price != 123.45 {
		t.Fatalf("Price = %v, want 123.45", c.Price)
	}
}

func TestZeroTimestampOmitted(t *testing.T) {
	// AggregatedAt zero -> proto Timestamp is nil.
	a := AggregatedPrice{AssetID: "weth", MedianPrice: 1}
	p := a.ToProto()
	if p.GetAggregatedAt() != nil {
		t.Fatalf("zero AggregatedAt should serialise as nil, got %v", p.GetAggregatedAt())
	}
}

func TestAgeSec(t *testing.T) {
	now := time.Now().UTC()
	r := RawPrice{SourceObservedAt: now.Add(-30 * time.Second)}
	if got := r.AgeSec(now); got != 30 {
		t.Fatalf("AgeSec = %d, want 30", got)
	}
	r2 := RawPrice{} // zero observed time
	if got := r2.AgeSec(now); got != 0 {
		t.Fatalf("AgeSec on zero observed should be 0, got %d", got)
	}
	r3 := RawPrice{SourceObservedAt: now.Add(5 * time.Second)} // future-dated
	if got := r3.AgeSec(now); got != 0 {
		t.Fatalf("AgeSec on future observed should clamp to 0, got %d", got)
	}
}

func TestIncludedCount(t *testing.T) {
	a := AggregatedPrice{Sources: []SourceContribution{
		{Included: true},
		{Included: false},
		{Included: true},
	}}
	if got := a.IncludedCount(); got != 2 {
		t.Fatalf("IncludedCount = %d, want 2", got)
	}
}

func TestAssetIDString(t *testing.T) {
	if got := AssetWETH.String(); got != "weth" {
		t.Fatalf("AssetWETH.String() = %q, want weth", got)
	}
}

func TestAssetClassIsValidAndString(t *testing.T) {
	if !AssetClassCrypto.IsValid() {
		t.Fatalf("AssetClassCrypto should be valid")
	}
	if AssetClassUnknown.IsValid() {
		t.Fatalf("AssetClassUnknown should not be valid")
	}
	if AssetClass(99).String() != "unknown" {
		t.Fatalf("out-of-range class should string to 'unknown'")
	}
}

func TestFreshnessPolicyIsValid(t *testing.T) {
	if !FreshnessPermissive.IsValid() {
		t.Fatalf("FreshnessPermissive should be valid")
	}
	if FreshnessUnknown.IsValid() {
		t.Fatalf("FreshnessUnknown should not be valid")
	}
	if FreshnessPolicy(99).String() != "unknown" {
		t.Fatalf("out-of-range policy should string to 'unknown'")
	}
}

func TestAssetSources(t *testing.T) {
	a, err := NewAsset("weth", "crypto", map[string]string{
		"coingecko": "weth",
		"binance":   "ETHUSDT",
	}, 30)
	if err != nil {
		t.Fatalf("NewAsset: %v", err)
	}
	got := a.Sources()
	if len(got) != 2 {
		t.Fatalf("Sources len = %d, want 2", len(got))
	}
}
