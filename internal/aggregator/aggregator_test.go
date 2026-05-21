package aggregator

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/sources"
)

// -- pure helpers --

func TestComputeMedian(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"single", []float64{42.5}, 42.5},
		{"odd", []float64{1, 2, 3}, 2},
		{"even", []float64{1, 2, 3, 4}, 2.5},
		{"out-of-order", []float64{3, 1, 2}, 2},
		{"identical", []float64{5, 5, 5}, 5},
		{"negative-and-positive", []float64{-1, 0, 1}, 0},
		{"large-spread", []float64{1, 1, 100}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeMedian(c.in)
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("computeMedian(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestComputeMedianDoesNotMutateInput(t *testing.T) {
	in := []float64{3, 1, 2}
	_ = computeMedian(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Fatalf("computeMedian mutated input: %v", in)
	}
}

func TestWindowBound(t *testing.T) {
	t0 := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	contrib := []models.SourceContribution{
		{FetchedAt: t0.Add(2 * time.Second)},
		{FetchedAt: t0},
		{FetchedAt: t0.Add(5 * time.Second)},
	}
	if got := windowBound(contrib, true); !got.Equal(t0) {
		t.Fatalf("earliest = %v, want %v", got, t0)
	}
	if got := windowBound(contrib, false); !got.Equal(t0.Add(5*time.Second)) {
		t.Fatalf("latest = %v, want %v", got, t0.Add(5*time.Second))
	}
	if got := windowBound(nil, true); !got.IsZero() {
		t.Fatalf("empty input should return zero time, got %v", got)
	}
}

// -- RunOnce: happy path + structural assertions --

// testAsset builds an Asset that covers three crypto sources, mirroring the
// configured weth asset.
func testAsset(t *testing.T) models.Asset {
	t.Helper()
	a, err := models.NewAsset("weth", "crypto", map[string]string{
		"coingecko":  "weth",
		"binance":    "ETHUSDT",
		"uniswap_v3": "0xpool",
	}, 30)
	if err != nil {
		t.Fatalf("NewAsset: %v", err)
	}
	return a
}

// defaultCfg returns a permissive aggregation config matching the service's
// production defaults.
func defaultCfg() config.AggregationConfig {
	return config.AggregationConfig{
		MinSources:       1,
		MaxDeviation:     0.10,
		FreshnessPolicy:  "permissive",
		StaleAfterCrypto: 300,
		StaleAfterRWA:    24 * 60 * 60,
	}
}

func TestRunOnceHappyPath(t *testing.T) {
	asset := testAsset(t)

	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 3451.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 3449.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)

	// Subscribe before the tick so we see the publish.
	sub, cancel := bus.Subscribe()
	defer cancel()

	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.MedianPrice != 3450.0 {
		t.Fatalf("median = %v, want 3450", out.MedianPrice)
	}
	if out.IncludedCount() != 3 {
		t.Fatalf("included = %d, want 3", out.IncludedCount())
	}

	// Persist call.
	calls := repo.PersistCalls()
	if len(calls) != 1 {
		t.Fatalf("PersistRound calls = %d, want 1", len(calls))
	}
	if len(calls[0].Raws) != 3 {
		t.Fatalf("Raws = %d, want 3", len(calls[0].Raws))
	}
	if calls[0].Agg.MedianPrice != 3450.0 {
		t.Fatalf("Agg.MedianPrice = %v, want 3450", calls[0].Agg.MedianPrice)
	}

	// Bus publish.
	select {
	case got := <-sub:
		if got.MedianPrice != 3450.0 {
			t.Fatalf("bus got %v, want 3450", got.MedianPrice)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("bus did not publish within 100ms")
	}
}

// -- Deviation guard --

func TestRunOnceDeviationGuardRejects(t *testing.T) {
	asset := testAsset(t)

	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 5000.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 5000.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 5000.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	repo.SeedLatest("weth", 3000.0) // 5000 vs 3000 = 66% deviation; max is 10%
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)

	_, err := agg.RunOnce(context.Background(), asset)
	if !errors.Is(err, models.ErrDeviationExceeded) {
		t.Fatalf("expected ErrDeviationExceeded, got %v", err)
	}
	if len(repo.PersistCalls()) > 1 {
		t.Fatalf("deviation guard should not persist; got %d calls", len(repo.PersistCalls()))
	}
}

func TestRunOnceDeviationGuardAccepts(t *testing.T) {
	asset := testAsset(t)

	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3100.0, time.Time{}, nil), // 3% delta vs 3000
		staticAdapter(models.SourceBinance, 3100.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 3100.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	repo.SeedLatest("weth", 3000.0)
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)

	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.MedianPrice != 3100.0 {
		t.Fatalf("median = %v, want 3100", out.MedianPrice)
	}
}

func TestRunOnceFirstTickSkipsDeviationGuard(t *testing.T) {
	// No seeded last; arbitrarily wild median should still be accepted.
	asset := testAsset(t)
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 99999.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 99999.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 99999.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)
	if _, err := agg.RunOnce(context.Background(), asset); err != nil {
		t.Fatalf("first tick should accept any price, got %v", err)
	}
}

// -- MinSources enforcement --

func TestRunOnceNotEnoughSources(t *testing.T) {
	asset := testAsset(t)
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 0, time.Time{}, sources.ErrUpstream),
		staticAdapter(models.SourceUniswapV3, 0, time.Time{}, sources.ErrUpstream),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	cfg := defaultCfg()
	cfg.MinSources = 2 // require at least 2

	agg := New(cfg, reg, repo, bus)

	_, err := agg.RunOnce(context.Background(), asset)
	if !errors.Is(err, models.ErrNotEnoughSources) {
		t.Fatalf("expected ErrNotEnoughSources, got %v", err)
	}
	if len(repo.PersistCalls()) != 0 {
		t.Fatalf("should not persist when MinSources unmet; got %d calls", len(repo.PersistCalls()))
	}
}

func TestRunOnceAllSourcesFail(t *testing.T) {
	asset := testAsset(t)
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 0, time.Time{}, sources.ErrUpstream),
		staticAdapter(models.SourceBinance, 0, time.Time{}, sources.ErrNoData),
		staticAdapter(models.SourceUniswapV3, 0, time.Time{}, sources.ErrUpstream),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)
	_, err := agg.RunOnce(context.Background(), asset)
	if !errors.Is(err, models.ErrNotEnoughSources) {
		t.Fatalf("expected ErrNotEnoughSources on all-fail, got %v", err)
	}
}

// -- Freshness policy --

func TestRunOnceFreshnessPermissiveKeepsStale(t *testing.T) {
	asset := testAsset(t)

	staleObs := time.Now().UTC().Add(-2 * time.Hour) // way past StaleAfterCrypto
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),    // fresh
		staticAdapter(models.SourceBinance, 3460.0, staleObs, nil),         // stale
		staticAdapter(models.SourceUniswapV3, 3445.0, time.Time{}, nil),    // fresh
	)
	repo := newMockRepo()
	bus := NewBus(8)

	cfg := defaultCfg() // permissive
	agg := New(cfg, reg, repo, bus)

	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.IncludedCount() != 3 {
		t.Fatalf("permissive should include all 3, got %d", out.IncludedCount())
	}
}

func TestRunOnceFreshnessStrictDropsStale(t *testing.T) {
	asset := testAsset(t)

	staleObs := time.Now().UTC().Add(-2 * time.Hour) // > StaleAfterCrypto (300s)
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),    // fresh
		staticAdapter(models.SourceBinance, 9999.0, staleObs, nil),         // stale outlier
		staticAdapter(models.SourceUniswapV3, 3445.0, time.Time{}, nil),    // fresh
	)
	repo := newMockRepo()
	bus := NewBus(8)

	cfg := defaultCfg()
	cfg.FreshnessPolicy = "strict"
	agg := New(cfg, reg, repo, bus)

	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.IncludedCount() != 2 {
		t.Fatalf("strict should drop the stale source; included = %d, want 2", out.IncludedCount())
	}
	// Median of {3450, 3445} = 3447.5 — the 9999 outlier is excluded.
	if out.MedianPrice != 3447.5 {
		t.Fatalf("median = %v, want 3447.5", out.MedianPrice)
	}
}

func TestRunOnceFreshnessStrictMissingObservationKept(t *testing.T) {
	// SourceObservedAt zero-value means "no upstream timestamp"; the strict
	// branch must not drop these — only sources whose observed time is past
	// the threshold get dropped.
	asset := testAsset(t)

	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 3460.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 3445.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	cfg := defaultCfg()
	cfg.FreshnessPolicy = "strict"
	agg := New(cfg, reg, repo, bus)

	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.IncludedCount() != 3 {
		t.Fatalf("zero-observation sources should be kept; included = %d, want 3", out.IncludedCount())
	}
}

// -- Misconfiguration --

func TestRunOnceRejectsBadPolicy(t *testing.T) {
	asset := testAsset(t)
	reg := newMockRegistry()
	repo := newMockRepo()
	bus := NewBus(8)

	cfg := defaultCfg()
	cfg.FreshnessPolicy = "neither-permissive-nor-strict"
	agg := New(cfg, reg, repo, bus)

	_, err := agg.RunOnce(context.Background(), asset)
	if err == nil {
		t.Fatalf("expected error from bad policy, got nil")
	}
}

// -- Adapter selection edge cases --

func TestRunOnceTreatsMissingAdapterAsFailure(t *testing.T) {
	asset := testAsset(t)

	// Registry only covers 2 of the asset's 3 sources.
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 3451.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)
	out, err := agg.RunOnce(context.Background(), asset)
	if err != nil {
		t.Fatalf("missing adapter should not fail the tick if MinSources met, got %v", err)
	}
	if out.IncludedCount() != 2 {
		t.Fatalf("included = %d, want 2 (the missing adapter is excluded)", out.IncludedCount())
	}
}

// -- Persistence failure --

func TestRunOncePersistFailureBubblesUp(t *testing.T) {
	asset := testAsset(t)
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 3451.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 3449.0, time.Time{}, nil),
	)
	repo := newMockRepo()
	repo.persistErr = errors.New("simulated db down")
	bus := NewBus(8)

	agg := New(defaultCfg(), reg, repo, bus)
	_, err := agg.RunOnce(context.Background(), asset)
	if err == nil || !errors.Is(err, repo.persistErr) {
		t.Fatalf("expected wrapped persist error, got %v", err)
	}
}

// -- lastPrice cache seeds from repo --

func TestGetLastSeedsFromRepoOnce(t *testing.T) {
	asset := testAsset(t)
	repo := newMockRepo()
	repo.SeedLatest("weth", 4000.0)

	// First tick: 3-source median 4100 (2.5% delta) — accepted.
	reg := newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 4100.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 4100.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 4100.0, time.Time{}, nil),
	)
	bus := NewBus(8)
	agg := New(defaultCfg(), reg, repo, bus)
	if _, err := agg.RunOnce(context.Background(), asset); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	// Second tick: median 5000 — that's 22% delta vs the just-set lastPrice
	// (4100), should now be rejected.
	reg = newMockRegistry(
		staticAdapter(models.SourceCoinGecko, 5000.0, time.Time{}, nil),
		staticAdapter(models.SourceBinance, 5000.0, time.Time{}, nil),
		staticAdapter(models.SourceUniswapV3, 5000.0, time.Time{}, nil),
	)
	agg.registry = reg
	_, err := agg.RunOnce(context.Background(), asset)
	if !errors.Is(err, models.ErrDeviationExceeded) {
		t.Fatalf("expected deviation rejection on second tick, got %v", err)
	}
}
