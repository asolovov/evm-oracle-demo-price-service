package grpc

import (
	"context"
	"fmt"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// Module owns the lifecycle of the gRPC server.
//
// The price.v1.PriceService handler is registered in task 10; for now this
// module just stands up the server framework + the standard gRPC health
// service so docker compose health checks work end-to-end.
type Module struct {
	config *config.GRPCConfig
	server *Server
}

// NewModule creates a new gRPC module instance.
func NewModule(cfg *config.GRPCConfig) *Module {
	return &Module{config: cfg}
}

// Name returns the module identifier.
func (m *Module) Name() string {
	return "grpc"
}

// Init starts the listener and registers the standard health service.
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

// Server exposes the underlying gRPC server so handler-registration code
// (added in task 10) can call e.g. pricev1.RegisterPriceServiceServer.
func (m *Module) Server() *Server {
	return m.server
}
