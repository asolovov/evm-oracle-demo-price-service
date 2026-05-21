package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/aggregator"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// stubRepo satisfies repository.PriceRepository for module-level lifecycle
// tests. The gRPC module itself doesn't call any repo methods — the
// dependency is forwarded to the handler — so a minimal stub is enough.
type stubRepo struct{}

func (stubRepo) PersistRound(_ context.Context, _ []models.RawPrice, _ models.AggregatedPrice) error {
	return nil
}

func (stubRepo) GetLatest(_ context.Context, _ models.AssetID) (models.AggregatedPrice, error) {
	return models.AggregatedPrice{}, models.ErrAssetNotTracked
}

func (stubRepo) GetHistory(_ context.Context, _ models.AssetID, _, _ time.Time, _ int) ([]models.AggregatedPrice, error) {
	return nil, nil
}

func (stubRepo) Ping(_ context.Context) error { return nil }

func testGRPCConfig() *config.GRPCConfig {
	return &config.GRPCConfig{
		Host:           "127.0.0.1",
		Port:           0,
		Timeout:        "5s",
		MaxSendMsgSize: 1024 * 1024,
		MaxRecvMsgSize: 1024 * 1024,
		Reflection:     true,
	}
}

func TestModuleLifecycle(t *testing.T) {
	bus := aggregator.NewBus(4)
	mod := NewModule(testGRPCConfig(), bus, stubRepo{})
	if mod.Name() != "grpc" {
		t.Fatalf("Name = %q, want grpc", mod.Name())
	}

	ctx := context.Background()
	if err := mod.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := mod.HealthCheck(ctx); err == nil {
		t.Fatalf("HealthCheck should fail before Start (server not running)")
	}
	if err := mod.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		if err := mod.Stop(ctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}()

	// Server() should expose the underlying *Server.
	if mod.Server() == nil {
		t.Fatalf("Server() returned nil after Init")
	}
	if err := mod.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck after Start: %v", err)
	}
}

func TestModuleHealthCheckBeforeInit(t *testing.T) {
	mod := NewModule(testGRPCConfig(), aggregator.NewBus(4), stubRepo{})
	if err := mod.HealthCheck(context.Background()); err == nil {
		t.Fatalf("HealthCheck should fail with 'not initialized' before Init")
	}
}

func TestModuleStopBeforeInitIsNoop(t *testing.T) {
	mod := NewModule(testGRPCConfig(), aggregator.NewBus(4), stubRepo{})
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Init should be a no-op, got %v", err)
	}
}

func TestModuleInitFailsOnBadConfig(t *testing.T) {
	bad := &config.GRPCConfig{Host: "127.0.0.1", Port: 0, Timeout: "not a duration"}
	mod := NewModule(bad, aggregator.NewBus(4), stubRepo{})
	if err := mod.Init(context.Background()); err == nil {
		t.Fatalf("Init should fail on bad timeout config")
	}
}

func TestModuleReflectionDisabled(t *testing.T) {
	cfg := testGRPCConfig()
	cfg.Reflection = false
	mod := NewModule(cfg, aggregator.NewBus(4), stubRepo{})
	if err := mod.Init(context.Background()); err != nil {
		t.Fatalf("Init with reflection=false: %v", err)
	}
	defer func() { _ = mod.Stop(context.Background()) }()
}
