package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func TestNewModuleParsesAssets(t *testing.T) {
	assets := []config.AssetConfig{
		{
			ID:                 "weth",
			Class:              "crypto",
			Symbols:            map[string]string{"coingecko": "weth", "binance": "ETHUSDT"},
			RefreshIntervalSec: 30,
		},
	}
	reg := newMockRegistry()
	repo := newMockRepo()
	cfg := config.AggregationConfig{
		MinSources: 1, MaxDeviation: 0.1, FreshnessPolicy: "permissive",
		StaleAfterCrypto: 300, StaleAfterRWA: 86400,
	}
	mod, err := NewModule(cfg, assets, reg, repo)
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if got := mod.Name(); got != "aggregator" {
		t.Fatalf("Name = %q, want aggregator", got)
	}
	if len(mod.Assets()) != 1 || mod.Assets()[0].ID != "weth" {
		t.Fatalf("Assets() did not parse correctly: %+v", mod.Assets())
	}
	if mod.Aggregator() == nil {
		t.Fatalf("Aggregator() returned nil")
	}
	if mod.Bus() == nil {
		t.Fatalf("Bus() returned nil")
	}
}

func TestNewModuleRejectsBadAsset(t *testing.T) {
	assets := []config.AssetConfig{
		{
			ID:                 "WETH", // uppercase — invalid
			Class:              "crypto",
			Symbols:            map[string]string{"coingecko": "weth"},
			RefreshIntervalSec: 30,
		},
	}
	reg := newMockRegistry()
	repo := newMockRepo()
	cfg := config.AggregationConfig{
		MinSources: 1, MaxDeviation: 0.1, FreshnessPolicy: "permissive",
		StaleAfterCrypto: 300, StaleAfterRWA: 86400,
	}
	_, err := NewModule(cfg, assets, reg, repo)
	if err == nil {
		t.Fatalf("expected error for invalid asset id, got nil")
	}
}

func TestModuleHealthCheckWarmingUp(t *testing.T) {
	mod, err := NewModule(
		config.AggregationConfig{
			MinSources: 1, MaxDeviation: 0.1, FreshnessPolicy: "permissive",
			StaleAfterCrypto: 300, StaleAfterRWA: 86400,
		},
		[]config.AssetConfig{{
			ID:                 "weth",
			Class:              "crypto",
			Symbols:            map[string]string{"coingecko": "weth"},
			RefreshIntervalSec: 30,
		}},
		newMockRegistry(),
		newMockRepo(),
	)
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	// Before any tick, HealthCheck reports warming-up.
	if err := mod.HealthCheck(context.Background()); err == nil {
		t.Fatalf("HealthCheck should fail with warming-up; got nil")
	}
}

func TestModuleHealthCheckHealthyAfterTick(t *testing.T) {
	mod, err := NewModule(
		config.AggregationConfig{
			MinSources: 1, MaxDeviation: 0.1, FreshnessPolicy: "permissive",
			StaleAfterCrypto: 300, StaleAfterRWA: 86400,
		},
		[]config.AssetConfig{{
			ID:                 "weth",
			Class:              "crypto",
			Symbols:            map[string]string{"coingecko": "weth"},
			RefreshIntervalSec: 30,
		}},
		newMockRegistry(staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil)),
		newMockRepo(),
	)
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}

	if _, err := mod.Aggregator().RunOnce(context.Background(), mod.Assets()[0]); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if err := mod.HealthCheck(context.Background()); err != nil {
		t.Fatalf("HealthCheck should pass after a tick, got %v", err)
	}
}

func TestModuleStartStopLifecycle(t *testing.T) {
	// Use a short refresh interval so we observe at least the first tick fire.
	mod, err := NewModule(
		config.AggregationConfig{
			MinSources: 1, MaxDeviation: 0.1, FreshnessPolicy: "permissive",
			StaleAfterCrypto: 300, StaleAfterRWA: 86400,
		},
		[]config.AssetConfig{{
			ID:                 "weth",
			Class:              "crypto",
			Symbols:            map[string]string{"coingecko": "weth"},
			RefreshIntervalSec: 1, // 1s; we'll stop well before the second tick.
		}},
		newMockRegistry(staticAdapter(models.SourceCoinGecko, 3450.0, time.Time{}, nil)),
		newMockRepo(),
	)
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}

	if err := mod.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := mod.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the first tick (fired immediately on Start) time to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := mod.HealthCheck(context.Background()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := mod.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected at least one tick within 500ms, HealthCheck = %v", err)
	}

	if err := mod.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
