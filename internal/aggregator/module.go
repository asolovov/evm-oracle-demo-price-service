package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/sources"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// Module owns the Aggregator + the per-asset scheduler.
//
// Each configured asset gets its own goroutine that fires RunOnce on the
// asset's RefreshIntervalSec cadence. The first tick fires immediately on
// Start so the gRPC handlers have data within the first 60 seconds of
// startup (acceptance criterion).
type Module struct {
	aggregator *Aggregator
	assets     []models.Asset
	bus        *Bus

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewModule wires the aggregator + bus + asset list.
func NewModule(
	agg config.AggregationConfig,
	assets []config.AssetConfig,
	registry *sources.Registry,
	repo repository.PriceRepository,
) (*Module, error) {
	parsed, err := parseAssets(assets)
	if err != nil {
		return nil, err
	}
	bus := NewBus(64)
	return &Module{
		aggregator: New(agg, registry, repo, bus),
		assets:     parsed,
		bus:        bus,
	}, nil
}

// Name returns the module identifier.
func (m *Module) Name() string { return "aggregator" }

// Init is a no-op; everything is wired in NewModule.
func (m *Module) Init(_ context.Context) error { return nil }

// Start spins up one goroutine per asset. Each goroutine fires RunOnce
// immediately, then on its asset's RefreshIntervalSec cadence.
func (m *Module) Start(ctx context.Context) error {
	logger.Log().Infof("starting %s module with %d asset(s)", m.Name(), len(m.assets))
	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	for _, asset := range m.assets {
		m.wg.Add(1)
		go m.loop(runCtx, asset)
	}
	return nil
}

// loop drives the per-asset tick. Each iteration uses a child context with
// the asset's refresh interval as a hard deadline so a slow source can't
// stall successive ticks.
func (m *Module) loop(ctx context.Context, asset models.Asset) {
	defer m.wg.Done()

	interval := time.Duration(asset.RefreshIntervalSec) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tick := func() {
		tickCtx, tickCancel := context.WithTimeout(ctx, interval)
		defer tickCancel()
		if _, err := m.aggregator.RunOnce(tickCtx, asset); err != nil {
			logger.Log().Warnf("aggregator: tick asset=%s: %v", asset.ID, err)
		}
	}

	tick() // first tick immediately
	for {
		select {
		case <-ctx.Done():
			logger.Log().Infof("aggregator: loop for asset=%s stopping", asset.ID)
			return
		case <-ticker.C:
			tick()
		}
	}
}

// Stop cancels every per-asset loop and waits for in-flight ticks to finish.
func (m *Module) Stop(_ context.Context) error {
	logger.Log().Infof("stopping %s module", m.Name())
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	return nil
}

// HealthCheck reports the aggregator healthy once at least one asset has a
// last price cached (i.e. one successful tick). Before that it reports
// "warming up" — surfaced as /readyz=503 by the healthz module.
func (m *Module) HealthCheck(_ context.Context) error {
	m.aggregator.mu.Lock()
	defer m.aggregator.mu.Unlock()
	if len(m.aggregator.lastPrice) == 0 {
		return fmt.Errorf("aggregator: warming up (no successful ticks yet)")
	}
	return nil
}

// Aggregator returns the underlying Aggregator. gRPC handlers use it for
// the GetPrice fall-through (cache miss → repo).
func (m *Module) Aggregator() *Aggregator { return m.aggregator }

// Bus returns the in-memory pub/sub. gRPC Subscribe handlers register here.
func (m *Module) Bus() *Bus { return m.bus }

// Assets returns the parsed asset list. Used by the gRPC handler to
// validate that the caller's asset id is in scope.
func (m *Module) Assets() []models.Asset { return m.assets }

// parseAssets converts the config-shaped asset list into validated domain
// values. Returns the first invalid asset's error.
func parseAssets(in []config.AssetConfig) ([]models.Asset, error) {
	out := make([]models.Asset, 0, len(in))
	for _, ac := range in {
		a, err := models.NewAsset(ac.ID, ac.Class, ac.Symbols, ac.RefreshIntervalSec)
		if err != nil {
			return nil, fmt.Errorf("parseAssets %s: %w", ac.ID, err)
		}
		out = append(out, a)
	}
	return out, nil
}
