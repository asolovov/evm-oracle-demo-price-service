package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// Module wraps the pgxpool lifecycle as an internal.Module so the application
// can register it alongside the gRPC server, scheduler, and healthz modules.
type Module struct {
	config *config.DatabaseConfig
	pool   *pgxpool.Pool
	repo   PriceRepository
}

// NewModule creates a new repository module instance.
func NewModule(cfg *config.DatabaseConfig) *Module {
	return &Module{config: cfg}
}

// Name returns the module identifier.
func (m *Module) Name() string {
	return "repository"
}

// Init creates the pgxpool and verifies connectivity. Sensitive fields
// (password) MUST NOT be logged — the prior template version did this; the
// rewrite removes that.
func (m *Module) Init(ctx context.Context) error {
	logger.Log().Infof("initializing %s module on %s:%d as %s (db=%s)",
		m.Name(), m.config.Host, m.config.Port, m.config.User, m.config.Name)

	cfg, err := pgxpool.ParseConfig(m.dsn())
	if err != nil {
		return fmt.Errorf("repository.Init: parse pool config: %w", err)
	}
	if m.config.MaxOpenConns > 0 {
		cfg.MaxConns = int32(m.config.MaxOpenConns)
	}
	if m.config.MaxIdleConns > 0 {
		cfg.MinConns = int32(m.config.MaxIdleConns)
	}
	if m.config.ConnMaxLifetime > 0 {
		cfg.MaxConnLifetime = time.Duration(m.config.ConnMaxLifetime) * time.Second
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("repository.Init: connect pool: %w", err)
	}
	m.pool = pool
	m.repo = NewPostgres(pool)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		m.pool = nil
		return fmt.Errorf("repository.Init: ping: %w", err)
	}

	logger.Log().Infof("%s module initialized successfully", m.Name())
	return nil
}

// Start is a no-op — the pool is ready after Init.
func (m *Module) Start(_ context.Context) error {
	return nil
}

// Stop closes the pool.
func (m *Module) Stop(_ context.Context) error {
	logger.Log().Infof("stopping %s module", m.Name())
	if m.pool != nil {
		m.pool.Close()
	}
	return nil
}

// HealthCheck verifies that the pool can still reach Postgres.
func (m *Module) HealthCheck(ctx context.Context) error {
	if m.pool == nil {
		return fmt.Errorf("repository: pool not initialised")
	}
	return m.pool.Ping(ctx)
}

// Repository returns the PriceRepository instance. Callers must invoke this
// only AFTER Init; before that it returns nil.
func (m *Module) Repository() PriceRepository {
	return m.repo
}

// dsn builds the pgx connection string. SSLMode is honoured per config; the
// password is interpolated but never logged (see Init).
func (m *Module) dsn() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		m.config.User, m.config.Password,
		m.config.Host, m.config.Port,
		m.config.Name, m.config.SSLMode,
	)
}
