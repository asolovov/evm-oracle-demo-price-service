package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestDefaultsSetEnvProd(t *testing.T) {
	t.Cleanup(func() { viper.Reset() })
	viper.Reset()

	setDefaults()

	if got := viper.GetString("env"); got != "prod" {
		t.Fatalf("expected env default prod, got %q", got)
	}
}

// loadDefaults builds a Scheme from the package defaults with the required
// keys filled in, so Validate-level tests don't trip on missing secrets.
func loadDefaults(t *testing.T) *Scheme {
	t.Helper()
	t.Cleanup(func() { viper.Reset() })
	viper.Reset()
	setDefaults()

	// Required-when-enabled secrets (mirrors what an operator supplies).
	viper.Set("database.password", "x")
	viper.Set("sources.uniswap_v3.api_key", "x")
	viper.Set("sources.alpha_vantage.api_key", "x")
	viper.Set("sources.eia.api_key", "x")
	viper.Set("sources.fred.api_key", "x")

	var s Scheme
	if err := viper.Unmarshal(&s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &s
}

// Phase 1 invariant: Alpha Vantage must NOT be mapped on spx/wti/hg (the
// equity-garbage path). Guards against a future edit reintroducing the bug.
func TestRWAAssetsDoNotUseAlphaVantageForCommodities(t *testing.T) {
	s := loadDefaults(t)
	byID := map[string]AssetConfig{}
	for _, a := range s.Assets {
		byID[a.ID] = a
	}
	for _, id := range []string{"spx", "wti", "hg"} {
		a, ok := byID[id]
		if !ok {
			t.Fatalf("asset %q missing from defaults", id)
		}
		if _, has := a.Symbols["alpha_vantage"]; has {
			t.Fatalf("asset %q must not map alpha_vantage (GLOBAL_QUOTE returns equities)", id)
		}
		if len(a.Symbols) == 0 {
			t.Fatalf("asset %q has zero sources — would abort startup", id)
		}
	}
}

// The default config (with required keys) must validate, and every RWA asset
// must retain >= 2 sources after the rework.
func TestDefaultConfigValidatesAndRWAHasTwoSources(t *testing.T) {
	s := loadDefaults(t)
	if err := s.Validate(); err != nil {
		t.Fatalf("default config should validate, got: %v", err)
	}
	rwa := map[string]bool{"xau": true, "xag": true, "spx": true, "wti": true, "hg": true}
	for _, a := range s.Assets {
		if rwa[a.ID] && len(a.Symbols) < 2 {
			t.Fatalf("RWA asset %q has %d sources, want >= 2", a.ID, len(a.Symbols))
		}
	}
}

// Twelve Data + Stooq are disabled by default (no working RWA free tier),
// so their absent keys must not block startup.
func TestTwelveDataAndStooqDisabledByDefault(t *testing.T) {
	s := loadDefaults(t)
	if s.Sources.TwelveData.Enabled {
		t.Fatalf("twelve_data should be disabled by default")
	}
	if s.Sources.Stooq.Enabled {
		t.Fatalf("stooq should be disabled by default")
	}
	// Validate must pass even though neither has an api_key set.
	if err := s.Validate(); err != nil {
		t.Fatalf("validate with TD/Stooq disabled and keyless: %v", err)
	}
}
