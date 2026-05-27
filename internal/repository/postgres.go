package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Postgres is the pgx-backed implementation of PriceRepository.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres wraps an existing connected pgxpool. The caller owns the pool's
// lifecycle (created and closed by the Module below).
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

// Ping verifies that the pool has a working connection.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// PersistRound inserts the N raw observations and the single aggregated row
// in one transaction. If any insert fails the transaction is rolled back and
// no rows become visible to readers.
func (p *Postgres) PersistRound(ctx context.Context, raws []models.RawPrice, agg models.AggregatedPrice) error {
	if len(raws) == 0 {
		return errors.New("repository.PersistRound: no raw rows to persist")
	}
	if err := agg.AssetID.Validate(); err != nil {
		return fmt.Errorf("repository.PersistRound: invalid agg asset id: %w", err)
	}

	breakdownJSON, err := encodeBreakdown(agg.Sources)
	if err != nil {
		return fmt.Errorf("repository.PersistRound: encode breakdown: %w", err)
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("repository.PersistRound: begin tx: %w", err)
	}
	// rollback is a no-op once Commit succeeds, so this is safe.
	defer func() { _ = tx.Rollback(ctx) }()

	const rawInsert = `
		INSERT INTO prices_raw
		    (asset_id, source, price, fetched_at, source_observed_at, raw_payload)
		VALUES
		    ($1, $2, $3, $4, $5, $6)`
	batch := &pgx.Batch{}
	for _, r := range raws {
		batch.Queue(
			rawInsert,
			string(r.AssetID),
			r.Source.String(),
			r.Price,
			r.FetchedAt,
			r.SourceObservedAt,
			r.RawPayload,
		)
	}
	br := tx.SendBatch(ctx, batch)
	for i := 0; i < len(raws); i++ {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("repository.PersistRound: insert raw row %d: %w", i, err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("repository.PersistRound: close batch: %w", err)
	}

	const aggInsert = `
		INSERT INTO prices_aggregated
		    (asset_id, median_price, source_count, source_breakdown_json,
		     aggregated_at, window_start, window_end)
		VALUES
		    ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := tx.Exec(ctx, aggInsert,
		string(agg.AssetID),
		agg.MedianPrice,
		agg.IncludedCount(),
		breakdownJSON,
		agg.AggregatedAt,
		agg.WindowStart,
		agg.WindowEnd,
	); err != nil {
		return fmt.Errorf("repository.PersistRound: insert aggregated row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository.PersistRound: commit: %w", err)
	}
	return nil
}

// GetLatest fetches the most recent aggregated row for an asset.
func (p *Postgres) GetLatest(ctx context.Context, assetID models.AssetID) (models.AggregatedPrice, error) {
	if err := assetID.Validate(); err != nil {
		return models.AggregatedPrice{}, err
	}

	const q = `
		SELECT median_price, source_breakdown_json, aggregated_at, window_start, window_end
		FROM prices_aggregated
		WHERE asset_id = $1
		ORDER BY aggregated_at DESC
		LIMIT 1`

	var (
		median      float64
		breakdown   []byte
		aggregated  time.Time
		windowStart time.Time
		windowEnd   time.Time
	)
	err := p.pool.QueryRow(ctx, q, string(assetID)).
		Scan(&median, &breakdown, &aggregated, &windowStart, &windowEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AggregatedPrice{}, fmt.Errorf("%w: %s", models.ErrAssetNotTracked, assetID)
	}
	if err != nil {
		return models.AggregatedPrice{}, fmt.Errorf("repository.GetLatest(%s): %w", assetID, err)
	}

	sources, err := decodeBreakdown(breakdown)
	if err != nil {
		return models.AggregatedPrice{}, fmt.Errorf("repository.GetLatest(%s): decode breakdown: %w", assetID, err)
	}

	return models.AggregatedPrice{
		AssetID:      assetID,
		MedianPrice:  median,
		AggregatedAt: aggregated,
		WindowStart:  windowStart,
		WindowEnd:    windowEnd,
		Sources:      sources,
	}, nil
}

// GetHistory returns aggregated rows in the given window, newest first.
func (p *Postgres) GetHistory(ctx context.Context, assetID models.AssetID, from, to time.Time, limit int) ([]models.AggregatedPrice, error) {
	if err := assetID.Validate(); err != nil {
		return nil, err
	}

	const base = `
		SELECT median_price, source_breakdown_json, aggregated_at, window_start, window_end
		FROM prices_aggregated
		WHERE asset_id = $1
		  AND aggregated_at BETWEEN $2 AND $3
		ORDER BY aggregated_at DESC`
	q := base
	args := []any{string(assetID), from, to}
	if limit > 0 {
		q += " LIMIT $4"
		args = append(args, limit)
	}

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("repository.GetHistory(%s): %w", assetID, err)
	}
	defer rows.Close()

	var out []models.AggregatedPrice
	for rows.Next() {
		var (
			median      float64
			breakdown   []byte
			aggregated  time.Time
			windowStart time.Time
			windowEnd   time.Time
		)
		if err := rows.Scan(&median, &breakdown, &aggregated, &windowStart, &windowEnd); err != nil {
			return nil, fmt.Errorf("repository.GetHistory(%s): scan: %w", assetID, err)
		}
		sources, err := decodeBreakdown(breakdown)
		if err != nil {
			return nil, fmt.Errorf("repository.GetHistory(%s): decode breakdown: %w", assetID, err)
		}
		out = append(out, models.AggregatedPrice{
			AssetID:      assetID,
			MedianPrice:  median,
			AggregatedAt: aggregated,
			WindowStart:  windowStart,
			WindowEnd:    windowEnd,
			Sources:      sources,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository.GetHistory(%s): rows err: %w", assetID, err)
	}
	return out, nil
}

// breakdownEntry is the JSONB shape persisted in
// prices_aggregated.source_breakdown_json. Compact field names keep the
// payload small at scale; the dashboard maps these to display labels.
type breakdownEntry struct {
	Source             string  `json:"source"`
	Price              float64 `json:"price"`
	FetchedAtUnix      int64   `json:"fetched_at"`
	SourceObservedUnix int64   `json:"source_observed_at,omitempty"`
	AgeSec             int64   `json:"age_sec"`
	Included           bool    `json:"included"`
}

func encodeBreakdown(sources []models.SourceContribution) ([]byte, error) {
	entries := make([]breakdownEntry, 0, len(sources))
	for _, s := range sources {
		entries = append(entries, breakdownEntry{
			Source:             s.Source.String(),
			Price:              s.Price,
			FetchedAtUnix:      s.FetchedAt.Unix(),
			SourceObservedUnix: zeroOrUnix(s.SourceObservedAt),
			AgeSec:             s.AgeSec,
			Included:           s.Included,
		})
	}
	return json.Marshal(entries)
}

func decodeBreakdown(raw []byte) ([]models.SourceContribution, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []breakdownEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := make([]models.SourceContribution, 0, len(entries))
	for _, e := range entries {
		src, err := models.SourceKindFromString(e.Source)
		if err != nil {
			// Unknown source values in stored JSONB indicate stale data from a
			// prior schema. We surface them as SourceUnknown rather than fail
			// the read — the dashboard renders them as "(unknown source)".
			src = models.SourceUnknown
		}
		out = append(out, models.SourceContribution{
			Source:           src,
			Price:            e.Price,
			FetchedAt:        time.Unix(e.FetchedAtUnix, 0).UTC(),
			SourceObservedAt: unixOrZero(e.SourceObservedUnix),
			AgeSec:           e.AgeSec,
			Included:         e.Included,
		})
	}
	return out, nil
}

func zeroOrUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func unixOrZero(u int64) time.Time {
	if u == 0 {
		return time.Time{}
	}
	return time.Unix(u, 0).UTC()
}
