// Package aggregator drives the per-tick fan-out + median + persist flow
// that the gRPC handlers expose.
//
// One tick:
//
//  1. For asset A, fan out to its registered source adapters in parallel
//     (errgroup with a per-tick context bounded by the asset's timeout).
//  2. Collect successful fetches; drop ErrNoData / ErrUpstream rows for
//     this tick (logged, surfaced in source breakdown with Included=false).
//  3. If at least MinSources fetches landed, compute the median across the
//     "included" set. Apply FreshnessPolicy first (strict drops sources
//     older than the per-class threshold; permissive keeps everything).
//  4. Compare the proposed median against the last accepted median for the
//     asset; reject (ErrDeviationExceeded) if |delta|/last > MaxDeviation.
//  5. Persist N raw rows + 1 aggregated row in a single transaction.
//  6. Publish to the in-memory Bus for live gRPC subscribers.
package aggregator

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/sources"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// SourceRegistry is the slice of the source registry the aggregator uses.
// Pulled into an interface so tests can substitute a hand-rolled registry of
// mock adapters without going through sources.NewRegistry. *sources.Registry
// satisfies it.
type SourceRegistry interface {
	Get(models.SourceKind) (sources.Adapter, bool)
}

// Aggregator coordinates one aggregation pass per asset.
type Aggregator struct {
	cfg      config.AggregationConfig
	registry SourceRegistry
	repo     repository.PriceRepository
	bus      *Bus

	// lastPrice caches the most recently accepted median per asset so the
	// deviation guard does not need a DB round-trip on every tick. Seeded
	// lazily from the repository on first lookup.
	mu        sync.Mutex
	lastPrice map[models.AssetID]float64
}

// New builds an Aggregator bound to a source registry, repository, and bus.
func New(cfg config.AggregationConfig, reg SourceRegistry, repo repository.PriceRepository, bus *Bus) *Aggregator {
	return &Aggregator{
		cfg:       cfg,
		registry:  reg,
		repo:      repo,
		bus:       bus,
		lastPrice: make(map[models.AssetID]float64),
	}
}

// RunOnce executes one aggregation pass for one asset and returns the
// resulting AggregatedPrice on success.
//
// Failure modes:
//   - ErrNotEnoughSources — fewer than MinSources adapters returned a price.
//   - ErrDeviationExceeded — median deviated from lastPrice by more than the
//     configured threshold. The raw rows are NOT persisted; the deviation
//     spike is logged + surfaces in metrics.
//   - any other error — transport / repository failure that prevents the
//     round from completing. The caller (scheduler) logs and moves on.
//
func (a *Aggregator) RunOnce(ctx context.Context, asset models.Asset) (models.AggregatedPrice, error) {
	policy, err := models.FreshnessPolicyFromString(a.cfg.FreshnessPolicy)
	if err != nil || !policy.IsValid() {
		return models.AggregatedPrice{}, fmt.Errorf("aggregator: bad freshness policy %q: %w", a.cfg.FreshnessPolicy, err)
	}

	// Step 1: fan-out fetch.
	raws, contributions := a.fetchAll(ctx, asset)
	tickEnd := time.Now().UTC()

	// Step 2: apply freshness policy (strict mode only).
	if policy == models.FreshnessStrict {
		a.applyStrictFreshness(asset, contributions, tickEnd)
	}

	includedPrices := make([]float64, 0, len(contributions))
	included := make([]models.SourceContribution, 0, len(contributions))
	for _, c := range contributions {
		if c.Included {
			includedPrices = append(includedPrices, c.Price)
			included = append(included, c)
		}
	}

	if len(includedPrices) < a.cfg.MinSources {
		return models.AggregatedPrice{}, fmt.Errorf("%w: asset=%s have=%d need=%d",
			models.ErrNotEnoughSources, asset.ID, len(includedPrices), a.cfg.MinSources)
	}

	// Step 3: median.
	median := computeMedian(includedPrices)

	// Step 4: deviation guard.
	last, hasLast := a.getLast(ctx, asset.ID)
	if hasLast && last > 0 {
		delta := math.Abs(median-last) / last
		if delta > a.cfg.MaxDeviation {
			return models.AggregatedPrice{}, fmt.Errorf("%w: asset=%s last=%g median=%g delta=%.4f max=%.4f",
				models.ErrDeviationExceeded, asset.ID, last, median, delta, a.cfg.MaxDeviation)
		}
	}

	// Step 5: assemble result + persist.
	agg := models.AggregatedPrice{
		AssetID:      asset.ID,
		MedianPrice:  median,
		AggregatedAt: tickEnd,
		WindowStart:  windowBound(included, true),
		WindowEnd:    windowBound(included, false),
		Sources:      contributions,
	}
	if err := a.repo.PersistRound(ctx, raws, agg); err != nil {
		return models.AggregatedPrice{}, fmt.Errorf("aggregator: persist asset=%s: %w", asset.ID, err)
	}

	// Step 6: cache last + publish.
	a.setLast(asset.ID, median)
	dropped := a.bus.Publish(agg)
	if dropped > 0 {
		logger.Log().Warnf("aggregator: %d subscribers dropped a publish for asset=%s", dropped, asset.ID)
	}

	logger.Log().Infof("aggregator: asset=%s median=%.6f sources=%d/%d",
		asset.ID, median, len(includedPrices), len(asset.Symbols))
	return agg, nil
}

// fetchAll fans out one fetch per adapter for `asset`. Returns the raws and
// the per-source contributions in stable iteration order (sorted by source
// string for deterministic logs / DB rows). Failures are logged and surface
// as contributions with Included=false.
func (a *Aggregator) fetchAll(ctx context.Context, asset models.Asset) ([]models.RawPrice, []models.SourceContribution) {
	type result struct {
		kind   models.SourceKind
		raw    models.RawPrice
		err    error
		symbol string
	}

	srcKinds := asset.Sources()
	sort.Slice(srcKinds, func(i, j int) bool {
		return srcKinds[i].String() < srcKinds[j].String()
	})

	results := make([]result, len(srcKinds))
	g, gctx := errgroup.WithContext(ctx)
	for i, kind := range srcKinds {
		i, kind := i, kind // capture
		adapter, ok := a.registry.Get(kind)
		if !ok {
			// Source disabled in config; record as missing contribution.
			results[i] = result{kind: kind, err: fmt.Errorf("adapter not registered")}
			continue
		}
		symbol, ok := asset.SymbolFor(kind)
		if !ok {
			results[i] = result{kind: kind, err: fmt.Errorf("symbol not configured")}
			continue
		}
		results[i].kind = kind
		results[i].symbol = symbol
		g.Go(func() error {
			raw, err := adapter.Fetch(gctx, symbol)
			results[i].raw = raw
			results[i].err = err
			return nil // never propagate; per-source failures are tolerated
		})
	}
	_ = g.Wait()

	now := time.Now().UTC()
	raws := make([]models.RawPrice, 0, len(results))
	contributions := make([]models.SourceContribution, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			// Per-source failure: logged at debug, surfaced as Included=false.
			logger.Log().Debugf("aggregator: fetch asset=%s source=%s: %v", asset.ID, r.kind, r.err)
			contributions = append(contributions, models.SourceContribution{
				Source:   r.kind,
				Included: false,
			})
			continue
		}
		// Adapters return a RawPrice without AssetID — they don't know which
		// asset they were polled for (Fetch only sees a source-specific
		// symbol). The aggregator owns the asset context, so it stamps the
		// id here before the row goes to PersistRound.
		r.raw.AssetID = asset.ID
		raws = append(raws, r.raw)
		contributions = append(contributions, models.SourceContribution{
			Source:           r.kind,
			Price:            r.raw.Price,
			FetchedAt:        r.raw.FetchedAt,
			SourceObservedAt: r.raw.SourceObservedAt,
			AgeSec:           r.raw.AgeSec(now),
			Included:         true,
		})
	}
	return raws, contributions
}

// applyStrictFreshness toggles Included=false on sources older than the
// per-class threshold. Permissive mode is a no-op (the spec wants ages
// recorded but never gating).
func (a *Aggregator) applyStrictFreshness(asset models.Asset, contributions []models.SourceContribution, now time.Time) {
	threshold := a.cfg.StaleAfterCrypto
	if asset.Class == models.AssetClassRWA {
		threshold = a.cfg.StaleAfterRWA
	}
	for i := range contributions {
		c := &contributions[i]
		if !c.Included {
			continue
		}
		if c.SourceObservedAt.IsZero() {
			continue
		}
		if int64(now.Sub(c.SourceObservedAt).Seconds()) > int64(threshold) {
			c.Included = false
		}
	}
}

// getLast returns the cached last accepted median for asset, seeding from
// the repository when empty.
func (a *Aggregator) getLast(ctx context.Context, id models.AssetID) (float64, bool) {
	a.mu.Lock()
	v, ok := a.lastPrice[id]
	a.mu.Unlock()
	if ok {
		return v, true
	}
	row, err := a.repo.GetLatest(ctx, id)
	if errors.Is(err, models.ErrAssetNotTracked) {
		return 0, false
	}
	if err != nil {
		// On read error fall back to "no prior price" rather than failing the
		// tick; deviation guard simply skips.
		logger.Log().Warnf("aggregator: load last price asset=%s: %v", id, err)
		return 0, false
	}
	a.setLast(id, row.MedianPrice)
	return row.MedianPrice, true
}

func (a *Aggregator) setLast(id models.AssetID, p float64) {
	a.mu.Lock()
	a.lastPrice[id] = p
	a.mu.Unlock()
}

// computeMedian returns the median of values. Caller must pass a non-empty
// slice; len(values) == 1 returns values[0].
func computeMedian(values []float64) float64 {
	cp := make([]float64, len(values))
	copy(cp, values)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

// windowBound returns the earliest (or latest) FetchedAt across included
// contributions. Used to fill prices_aggregated.window_{start,end}.
func windowBound(included []models.SourceContribution, earliest bool) time.Time {
	if len(included) == 0 {
		return time.Time{}
	}
	bound := included[0].FetchedAt
	for _, c := range included[1:] {
		if earliest {
			if c.FetchedAt.Before(bound) {
				bound = c.FetchedAt
			}
		} else {
			if c.FetchedAt.After(bound) {
				bound = c.FetchedAt
			}
		}
	}
	return bound
}
