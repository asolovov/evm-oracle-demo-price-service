package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/sources"
)

// mockRegistry is a tiny SourceRegistry implementation backed by a map.
// Tests build one with newMockRegistry(...).
type mockRegistry struct {
	adapters map[models.SourceKind]sources.Adapter
}

func newMockRegistry(adapters ...sources.Adapter) *mockRegistry {
	m := &mockRegistry{adapters: make(map[models.SourceKind]sources.Adapter, len(adapters))}
	for _, a := range adapters {
		m.adapters[a.Kind()] = a
	}
	return m
}

func (r *mockRegistry) Get(k models.SourceKind) (sources.Adapter, bool) {
	a, ok := r.adapters[k]
	return a, ok
}

// mockAdapter satisfies sources.Adapter via a configurable Fetch function.
type mockAdapter struct {
	kind  models.SourceKind
	fetch func(ctx context.Context, symbol string) (models.RawPrice, error)
}

func newMockAdapter(kind models.SourceKind, fn func(context.Context, string) (models.RawPrice, error)) *mockAdapter {
	return &mockAdapter{kind: kind, fetch: fn}
}

// staticAdapter returns the same RawPrice (or err) every Fetch call.
func staticAdapter(kind models.SourceKind, price float64, observed time.Time, err error) *mockAdapter {
	return newMockAdapter(kind, func(_ context.Context, _ string) (models.RawPrice, error) {
		if err != nil {
			return models.RawPrice{}, err
		}
		now := time.Now().UTC()
		obs := observed
		if obs.IsZero() {
			obs = now
		}
		return models.RawPrice{
			Source:           kind,
			Price:            price,
			FetchedAt:        now,
			SourceObservedAt: obs,
		}, nil
	})
}

func (a *mockAdapter) Kind() models.SourceKind { return a.kind }
func (a *mockAdapter) Fetch(ctx context.Context, symbol string) (models.RawPrice, error) {
	return a.fetch(ctx, symbol)
}

// mockRepo implements repository.PriceRepository for aggregator tests.
type mockRepo struct {
	mu sync.Mutex

	persistErr error
	getErr     error

	latest       map[models.AssetID]models.AggregatedPrice
	persistCalls []persistCall
}

type persistCall struct {
	Raws []models.RawPrice
	Agg  models.AggregatedPrice
}

func newMockRepo() *mockRepo {
	return &mockRepo{latest: make(map[models.AssetID]models.AggregatedPrice)}
}

func (r *mockRepo) PersistRound(_ context.Context, raws []models.RawPrice, agg models.AggregatedPrice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.persistErr != nil {
		return r.persistErr
	}
	r.persistCalls = append(r.persistCalls, persistCall{Raws: raws, Agg: agg})
	r.latest[agg.AssetID] = agg
	return nil
}

func (r *mockRepo) GetLatest(_ context.Context, assetID models.AssetID) (models.AggregatedPrice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return models.AggregatedPrice{}, r.getErr
	}
	v, ok := r.latest[assetID]
	if !ok {
		return models.AggregatedPrice{}, models.ErrAssetNotTracked
	}
	return v, nil
}

func (r *mockRepo) GetHistory(_ context.Context, _ models.AssetID, _, _ time.Time, _ int) ([]models.AggregatedPrice, error) {
	return nil, nil
}

func (r *mockRepo) Ping(_ context.Context) error { return nil }

// PersistCalls returns a snapshot of recorded persist calls. Tests read this
// to assert what the aggregator wrote.
func (r *mockRepo) PersistCalls() []persistCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]persistCall, len(r.persistCalls))
	copy(out, r.persistCalls)
	return out
}

// SeedLatest pre-populates lastPrice for an asset; the aggregator's
// deviation guard will compare against this on the next tick.
func (r *mockRepo) SeedLatest(id models.AssetID, price float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest[id] = models.AggregatedPrice{AssetID: id, MedianPrice: price}
}
