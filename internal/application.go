// Package internal wires the application together.
//
// Architecture rules 1 + 2 — cmd/ does only Cobra/Viper init; this package
// creates, wires, and lifecycles every component. The service's modules are
// repository (postgres), aggregator (with embedded source adapters and
// scheduler), gRPC server, and healthz HTTP. Each module satisfies the
// module.Module interface registered with module.Manager.
package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/module"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/version"
)

// App is the root application handle.
type App struct {
	config  *config.Scheme
	version *version.Version
	modules *module.Manager

	repo repository.PriceRepository
}

// NewApplication returns a fresh App with an empty config and module manager.
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

// Modules returns the module manager (useful for HealthCheck wiring).
func (app *App) Modules() *module.Manager {
	return app.modules
}

// Init validates configuration, then registers and initialises every module.
// Modules are registered in dependency order so the manager initialises them
// in that order too (see module.Manager.Register + InitAll).
//
//nolint:gocyclo // module-wiring orchestration; decompose if it grows further.
func (app *App) Init() error {
	if err := app.config.Validate(); err != nil {
		return fmt.Errorf("config validate: %w", err)
	}

	ctx := context.Background()

	// 1. Repository (Postgres). Owns the `evm_price` database; everything
	//    else needs its handle.
	repoModule := repository.NewModule(&app.config.Database)
	if err := repoModule.Init(ctx); err != nil {
		return fmt.Errorf("init repository: %w", err)
	}
	app.modules.Register(repoModule)
	app.repo = repoModule.Repository()

	// 2. Aggregator (with embedded sources + scheduler). Wired in task 8.
	// 3. gRPC server. Wired in task 10.
	// 4. Healthz HTTP. Wired in task 12.
	// TODO: register the modules above once their implementations land.

	logger.Log().Infof("application initialised: %d module(s) registered", app.modules.Count())
	return nil
}

// Serve starts every registered module and blocks until SIGINT / SIGTERM /
// SIGQUIT. Returns nil on graceful shutdown.
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

// Stop runs StopAll with a bounded shutdown deadline. Always invoked from the
// Cobra PostRun hook so it fires even when Serve returns an error.
func (app *App) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return app.modules.StopAll(ctx)
}
