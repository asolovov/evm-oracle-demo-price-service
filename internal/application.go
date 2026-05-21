// Package internal wires the application together.
//
// Architecture rules 1 + 2 — cmd/ does only Cobra/Viper init; this package
// creates, wires, and lifecycles every component. The service's modules,
// in registration order:
//
//  1. repository  (postgres pool, owns evm_price)
//  2. aggregator  (source registry + scheduler + in-memory bus)
//  3. grpc        (price.v1.PriceService + reflection + grpc health)
//  4. healthz     (HTTP /healthz, /readyz, /metrics)
//
// Each module satisfies module.Module; the module.Manager handles the
// init / start / stop / health-check orchestration.
package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/aggregator"
	grpcmod "github.com/asolovov/evm-oracle-demo-price-service/internal/grpc"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/healthz"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/module"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/sources"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/version"
)

// App is the root application handle.
type App struct {
	config  *config.Scheme
	version *version.Version
	modules *module.Manager
}

// NewApplication returns a fresh App with an empty config + module manager.
// Cobra populates app.config via Viper before Init runs.
func NewApplication() (*App, error) {
	ver, err := version.NewVersion()
	if err != nil {
		return nil, fmt.Errorf("init app version: %w", err)
	}
	return &App{
		config:  &config.Scheme{},
		version: ver,
		modules: module.NewManager(),
	}, nil
}

// Config returns the configuration Cobra populates.
func (app *App) Config() *config.Scheme {
	return app.config
}

// Version returns the build-time version string.
func (app *App) Version() string {
	return app.version.String()
}

// Modules returns the module manager.
func (app *App) Modules() *module.Manager {
	return app.modules
}

// Init validates configuration, then constructs + initialises each module in
// dependency order (repository -> aggregator -> grpc -> healthz). Module
// constructors that take dependencies receive them here; nothing wires
// itself.
func (app *App) Init() error {
	if err := app.config.Validate(); err != nil {
		return fmt.Errorf("config validate: %w", err)
	}

	ctx := context.Background()

	// 1. Repository.
	repoMod := repository.NewModule(&app.config.Database)
	if err := repoMod.Init(ctx); err != nil {
		return fmt.Errorf("init repository: %w", err)
	}
	app.modules.Register(repoMod)
	repo := repoMod.Repository()

	// 2. Source adapters + aggregator.
	registry, err := sources.NewRegistry(app.config.Sources)
	if err != nil {
		return fmt.Errorf("build source registry: %w", err)
	}
	aggMod, err := aggregator.NewModule(app.config.Aggregation, app.config.Assets, registry, repo)
	if err != nil {
		return fmt.Errorf("build aggregator: %w", err)
	}
	if err := aggMod.Init(ctx); err != nil {
		return fmt.Errorf("init aggregator: %w", err)
	}
	app.modules.Register(aggMod)

	// 3. gRPC server.
	grpcModule := grpcmod.NewModule(&app.config.GRPC, aggMod, repo)
	if err := grpcModule.Init(ctx); err != nil {
		return fmt.Errorf("init grpc: %w", err)
	}
	app.modules.Register(grpcModule)

	// 4. Healthz HTTP. Registered last so it can poll the others.
	healthzMod := healthz.NewModule(&app.config.Healthz, app.modules)
	if err := healthzMod.Init(ctx); err != nil {
		return fmt.Errorf("init healthz: %w", err)
	}
	app.modules.Register(healthzMod)

	logger.Log().Infof("application initialised: %d module(s) registered", app.modules.Count())
	return nil
}

// Serve starts every registered module and blocks until a shutdown signal.
func (app *App) Serve() error {
	ctx := context.Background()
	if err := app.modules.StartAll(ctx); err != nil {
		return fmt.Errorf("start modules: %w", err)
	}
	logger.Log().Info("application is running, press Ctrl+C to stop")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-quit
	logger.Log().Info("shutdown signal received, stopping gracefully…")
	return nil
}

// Stop drives StopAll with a bounded shutdown deadline.
func (app *App) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return app.modules.StopAll(ctx)
}
