package grpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc/reflection"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/aggregator"
	pricev1 "github.com/asolovov/evm-oracle-demo-price-service/internal/genproto/price/v1"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/grpc/handlers"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// Module owns the gRPC server lifecycle and registers the PriceService
// handler.
type Module struct {
	config *config.GRPCConfig
	bus    *aggregator.Bus
	repo   repository.PriceRepository
	server *Server
}

// NewModule wires the gRPC module with its handler dependencies.
func NewModule(cfg *config.GRPCConfig, bus *aggregator.Bus, repo repository.PriceRepository) *Module {
	return &Module{config: cfg, bus: bus, repo: repo}
}

// Name returns the module identifier.
func (m *Module) Name() string { return "grpc" }

// Init creates the listener, registers the standard health service, the
// price.v1.PriceService handler, and (when enabled) server reflection for
// grpcurl-driven debugging.
func (m *Module) Init(_ context.Context) error {
	logger.Log().Infof("initializing %s module on %s:%d", m.Name(), m.config.Host, m.config.Port)

	server, err := NewServer(m.config)
	if err != nil {
		return fmt.Errorf("create grpc server: %w", err)
	}
	m.server = server

	if err := m.server.RegisterHealthService(); err != nil {
		return fmt.Errorf("register health service: %w", err)
	}

	priceHandler := handlers.NewPriceServiceHandler(m.bus, m.repo)
	pricev1.RegisterPriceServiceServer(m.server.Server(), priceHandler)
	logger.Log().Info("registered price.v1.PriceService handler")

	if m.config.Reflection {
		reflection.Register(m.server.Server())
		logger.Log().Info("grpc server reflection enabled")
	}

	logger.Log().Infof("%s module initialized successfully", m.Name())
	return nil
}

// Start begins gRPC server operation (non-blocking).
func (m *Module) Start(_ context.Context) error {
	m.server.MarkRunning()
	go func() {
		if err := m.server.Serve(); err != nil {
			logger.Log().Errorf("grpc server error: %v", err)
		}
	}()
	logger.Log().Infof("grpc server listening on %s:%d", m.config.Host, m.config.Port)
	return nil
}

// Stop gracefully shuts down the gRPC server.
func (m *Module) Stop(_ context.Context) error {
	logger.Log().Infof("stopping %s module", m.Name())
	if m.server != nil {
		m.server.GracefulStop()
		logger.Log().Info("grpc server stopped gracefully")
	}
	return nil
}

// HealthCheck verifies the server is running.
func (m *Module) HealthCheck(_ context.Context) error {
	if m.server == nil {
		return fmt.Errorf("grpc server not initialised")
	}
	if !m.server.IsRunning() {
		return fmt.Errorf("grpc server not running")
	}
	return nil
}

// Server exposes the underlying server for callers that need it.
func (m *Module) Server() *Server { return m.server }
