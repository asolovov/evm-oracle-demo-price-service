package grpc

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/asolovov/evm-oracle-demo-price-service/config"
)

// newTestServer builds a Server bound to ":0" so tests don't collide on
// fixed ports.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(&config.GRPCConfig{
		Host:           "127.0.0.1",
		Port:           0,
		Timeout:        "5s",
		MaxSendMsgSize: 1024 * 1024,
		MaxRecvMsgSize: 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestNewServerSucceeds(t *testing.T) {
	s := newTestServer(t)
	defer s.GracefulStop()

	if s.Server() == nil {
		t.Fatalf("Server() = nil")
	}
	if s.IsRunning() {
		t.Fatalf("server should not be running before MarkRunning()")
	}
}

func TestNewServerRejectsBadTimeout(t *testing.T) {
	_, err := NewServer(&config.GRPCConfig{
		Host:    "127.0.0.1",
		Port:    0,
		Timeout: "definitely not a duration",
	})
	if err == nil {
		t.Fatalf("expected error for bad timeout, got nil")
	}
}

func TestRegisterHealthService(t *testing.T) {
	s := newTestServer(t)
	defer s.GracefulStop()

	if err := s.RegisterHealthService(); err != nil {
		t.Fatalf("RegisterHealthService: %v", err)
	}
}

func TestMarkRunningTogglesIsRunning(t *testing.T) {
	s := newTestServer(t)
	defer s.GracefulStop()

	s.MarkRunning()
	if !s.IsRunning() {
		t.Fatalf("IsRunning() = false after MarkRunning()")
	}
}

func TestLoggingInterceptorRoundTrip(t *testing.T) {
	interceptor := loggingInterceptor()

	want := "hello"
	got, err := interceptor(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: "/test/Method"},
		func(_ context.Context, _ any) (any, error) { return want, nil },
	)
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if got != want {
		t.Fatalf("interceptor returned %v, want %v", got, want)
	}
}

func TestLoggingInterceptorWithError(t *testing.T) {
	interceptor := loggingInterceptor()
	wantErr := errors.New("boom")
	_, err := interceptor(
		context.Background(),
		"req",
		nil, // info nil hits the "unknown" branch
		func(_ context.Context, _ any) (any, error) { return nil, wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestRecoveryInterceptorCatchesPanic(t *testing.T) {
	interceptor := recoveryInterceptor()
	_, err := interceptor(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: "/test/Boom"},
		func(_ context.Context, _ any) (any, error) {
			panic("kaboom")
		},
	)
	if err == nil {
		t.Fatalf("recovery interceptor did not produce an error from a panic")
	}
}

func TestRecoveryInterceptorPassesNormalErrors(t *testing.T) {
	interceptor := recoveryInterceptor()
	wantErr := errors.New("normal error")
	_, err := interceptor(
		context.Background(),
		"req",
		&grpc.UnaryServerInfo{FullMethod: "/test/Method"},
		func(_ context.Context, _ any) (any, error) { return nil, wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
