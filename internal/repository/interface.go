// Package repository persists aggregated prices and raw source observations.
//
// The price-service owns the `evm_price` database (architecture rule 7).
// This package is the only place that knows about SQL — the rest of the
// service works exclusively with domain models from internal/models.
package repository

import (
	"context"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// PriceRepository is the persistence surface used by the aggregator and the
// gRPC handlers. Implementations must be safe for concurrent use.
type PriceRepository interface {
	// PersistRound atomically writes one complete aggregation round —
	// N raw rows in `prices_raw` plus one aggregated row in
	// `prices_aggregated` — inside a single transaction. The caller MUST
	// pass non-empty `raws` and a non-nil `agg`. Returns an error if the
	// transaction fails; on success no partial state is visible to readers.
	PersistRound(ctx context.Context, raws []models.RawPrice, agg models.AggregatedPrice) error

	// GetLatest returns the most recent AggregatedPrice for one asset.
	// Returns models.ErrAssetNotTracked when no rows exist for the asset.
	GetLatest(ctx context.Context, assetID models.AssetID) (models.AggregatedPrice, error)

	// GetHistory returns AggregatedPrice rows within [from, to], descending
	// by aggregated_at. limit <= 0 means no limit; callers should always
	// pass a positive cap to avoid unbounded scans.
	GetHistory(ctx context.Context, assetID models.AssetID, from, to time.Time, limit int) ([]models.AggregatedPrice, error)

	// Ping is used by the healthz module to surface DB connectivity.
	Ping(ctx context.Context) error
}
