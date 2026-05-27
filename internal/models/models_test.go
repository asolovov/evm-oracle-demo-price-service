package models

import (
	"errors"
	"strings"
	"testing"
)

func TestAssetIDValidate(t *testing.T) {
	cases := []struct {
		name    string
		id      AssetID
		wantErr bool
	}{
		{"valid weth", "weth", false},
		{"valid hg", "hg", false},
		{"empty rejected", "", true},
		{"uppercase rejected", "WETH", true},
		{"mixed case rejected", "Weth", true},
		{"whitespace rejected", "we th", true},
		{"newline rejected", "weth\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.id.Validate()
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.wantErr && err != nil && !errors.Is(err, ErrInvalidAssetID) {
				t.Fatalf("error %v does not wrap ErrInvalidAssetID", err)
			}
		})
	}
}

func TestSourceKindRoundTrip(t *testing.T) {
	for _, k := range AllSources() {
		s := k.String()
		parsed, err := SourceKindFromString(s)
		if err != nil {
			t.Fatalf("%v.String() = %q, FromString returned %v", k, s, err)
		}
		if parsed != k {
			t.Fatalf("round-trip: %v -> %q -> %v", k, s, parsed)
		}
	}
}

func TestSourceKindFromStringUnknown(t *testing.T) {
	_, err := SourceKindFromString("not-a-source")
	if err == nil || !errors.Is(err, ErrUnknownSourceKind) {
		t.Fatalf("expected ErrUnknownSourceKind, got %v", err)
	}
	if !strings.Contains(err.Error(), "not-a-source") {
		t.Fatalf("error should mention the bad input, got %q", err.Error())
	}
}

func TestSourceKindAssetClass(t *testing.T) {
	crypto := []SourceKind{SourceCoinGecko, SourceBinance, SourceUniswapV3}
	rwa := []SourceKind{SourceAlphaVantage, SourceTwelveData, SourceStooq}
	for _, k := range crypto {
		if got := k.AssetClass(); got != AssetClassCrypto {
			t.Fatalf("%v.AssetClass() = %v, want %v", k, got, AssetClassCrypto)
		}
	}
	for _, k := range rwa {
		if got := k.AssetClass(); got != AssetClassRWA {
			t.Fatalf("%v.AssetClass() = %v, want %v", k, got, AssetClassRWA)
		}
	}
	if SourceUnknown.AssetClass() != AssetClassUnknown {
		t.Fatalf("SourceUnknown.AssetClass() must be AssetClassUnknown")
	}
}

func TestNewAssetRejectsCrossClassSource(t *testing.T) {
	// crypto asset wired to an RWA source should fail validation — the
	// aggregator does not mix classes.
	_, err := NewAsset("weth", "crypto", map[string]string{
		"coingecko":     "weth",
		"alpha_vantage": "WETH", // crypto asset, RWA source — bad
	}, 30)
	if err == nil {
		t.Fatalf("expected NewAsset to reject cross-class source")
	}
	if !errors.Is(err, ErrInvalidAsset) {
		t.Fatalf("error should wrap ErrInvalidAsset, got %v", err)
	}
}

func TestNewAssetAcceptsValid(t *testing.T) {
	a, err := NewAsset("weth", "crypto", map[string]string{
		"coingecko": "weth",
		"binance":   "ETHUSDT",
	}, 30)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if a.ID != "weth" {
		t.Fatalf("ID = %q, want %q", a.ID, "weth")
	}
	if a.Class != AssetClassCrypto {
		t.Fatalf("Class = %v, want %v", a.Class, AssetClassCrypto)
	}
	if _, ok := a.SymbolFor(SourceCoinGecko); !ok {
		t.Fatalf("SymbolFor(SourceCoinGecko) should resolve")
	}
}

func TestFreshnessPolicyRoundTrip(t *testing.T) {
	for _, p := range []FreshnessPolicy{FreshnessPermissive, FreshnessStrict} {
		parsed, err := FreshnessPolicyFromString(p.String())
		if err != nil {
			t.Fatalf("FromString(%q): %v", p.String(), err)
		}
		if parsed != p {
			t.Fatalf("round-trip: %v -> %q -> %v", p, p.String(), parsed)
		}
	}
}
