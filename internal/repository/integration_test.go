//go:build integration

// Repository integration test gated behind the `integration` build tag.
// Run with: make test-integration  (or `go test -tags=integration ./...`).
//
// Requires a working docker daemon. The test spins a Postgres 16-alpine
// container, applies `migrations/0001_init.up.sql`, persists one full
// aggregation round, and reads it back via GetLatest. Catches schema /
// query / JSONB round-trip regressions that the unit tests can't.

package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func TestPostgresRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("evm_price"),
		postgres.WithUsername("price_user"),
		postgres.WithPassword("price_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("conn string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	// Apply 0001_init by executing the SQL file directly. The migrations dir
	// is two levels up from internal/repository/.
	upFile := filepath.Join("..", "..", "migrations", "0001_init.up.sql")
	upSQL, err := os.ReadFile(upFile)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	repo := NewPostgres(pool)
	if err := repo.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Persist a round.
	now := time.Now().UTC().Truncate(time.Microsecond) // pg storage precision
	raws := []models.RawPrice{
		{
			AssetID:          "weth",
			Source:           models.SourceCoinGecko,
			Price:            3450.0,
			FetchedAt:        now,
			SourceObservedAt: now,
			RawPayload:       []byte(`{"weth":{"usd":3450}}`),
		},
		{
			AssetID:          "weth",
			Source:           models.SourceBinance,
			Price:            3451.0,
			FetchedAt:        now.Add(2 * time.Millisecond),
			SourceObservedAt: now.Add(2 * time.Millisecond),
			RawPayload:       []byte(`{"symbol":"ETHUSDT","price":"3451"}`),
		},
	}
	agg := models.AggregatedPrice{
		AssetID:      "weth",
		MedianPrice:  3450.5,
		AggregatedAt: now,
		WindowStart:  now,
		WindowEnd:    now.Add(2 * time.Millisecond),
		Sources: []models.SourceContribution{
			{
				Source:           models.SourceCoinGecko,
				Price:            3450,
				FetchedAt:        now,
				SourceObservedAt: now,
				AgeSec:           1,
				Included:         true,
			},
			{
				Source:           models.SourceBinance,
				Price:            3451,
				FetchedAt:        now.Add(2 * time.Millisecond),
				SourceObservedAt: now.Add(2 * time.Millisecond),
				AgeSec:           1,
				Included:         true,
			},
		},
	}
	if err := repo.PersistRound(ctx, raws, agg); err != nil {
		t.Fatalf("PersistRound: %v", err)
	}

	// Read back via GetLatest.
	got, err := repo.GetLatest(ctx, "weth")
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if got.MedianPrice != 3450.5 {
		t.Fatalf("MedianPrice = %v, want 3450.5", got.MedianPrice)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("Sources len = %d, want 2", len(got.Sources))
	}
	if got.Sources[0].Source != models.SourceCoinGecko {
		t.Fatalf("Sources[0].Source = %v, want CoinGecko", got.Sources[0].Source)
	}

	// GetLatest for an unknown asset returns ErrAssetNotTracked.
	if _, err := repo.GetLatest(ctx, "missing"); err == nil {
		t.Fatalf("expected ErrAssetNotTracked for missing asset, got nil")
	}

	// GetHistory window.
	rows, err := repo.GetHistory(ctx, "weth", now.Add(-time.Hour), now.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetHistory rows = %d, want 1", len(rows))
	}
}
