package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// mockRepo is a hand-rolled PriceRepository for handler-level tests.
type mockRepo struct {
	mu sync.Mutex

	latest   map[models.AssetID]models.AggregatedPrice
	getErr   error
	getCalls []models.AssetID
}

func newMockRepo() *mockRepo {
	return &mockRepo{latest: make(map[models.AssetID]models.AggregatedPrice)}
}

func (r *mockRepo) PersistRound(_ context.Context, _ []models.RawPrice, _ models.AggregatedPrice) error {
	return nil
}

func (r *mockRepo) GetLatest(_ context.Context, assetID models.AssetID) (models.AggregatedPrice, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getCalls = append(r.getCalls, assetID)
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

// Seed lets a test pre-populate the repo before exercising the handler.
func (r *mockRepo) Seed(id models.AssetID, price float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latest[id] = models.AggregatedPrice{
		AssetID:      id,
		MedianPrice:  price,
		AggregatedAt: time.Now().UTC(),
	}
}
