// Package handlers contains the gRPC service implementations.
package handlers

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/aggregator"
	pricev1 "github.com/asolovov/evm-oracle-demo-price-service/internal/genproto/price/v1"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/repository"
	"github.com/asolovov/evm-oracle-demo-price-service/pkg/logger"
)

// PriceServiceHandler implements pricev1.PriceServiceServer.
//
// GetPrice reads the most recently persisted aggregation from the
// repository. Subscribe pushes an initial per-asset snapshot from the
// repository, then forwards live updates from the aggregator's Bus.
type PriceServiceHandler struct {
	pricev1.UnimplementedPriceServiceServer
	bus  *aggregator.Bus
	repo repository.PriceRepository
}

// NewPriceServiceHandler wires a handler.
func NewPriceServiceHandler(bus *aggregator.Bus, repo repository.PriceRepository) *PriceServiceHandler {
	return &PriceServiceHandler{bus: bus, repo: repo}
}

// GetPrice returns the most recent AggregatedPrice for one asset.
//
// Maps domain errors to gRPC status codes:
//   - models.ErrAssetNotTracked -> NotFound
//   - models.ErrInvalidAssetID  -> InvalidArgument
//   - any other error           -> Internal
func (h *PriceServiceHandler) GetPrice(ctx context.Context, req *pricev1.GetPriceRequest) (*pricev1.AggregatedPrice, error) {
	id := models.AssetID(req.GetAssetId())
	if err := id.Validate(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "asset_id: %v", err)
	}
	row, err := h.repo.GetLatest(ctx, id)
	if errors.Is(err, models.ErrAssetNotTracked) {
		return nil, status.Errorf(codes.NotFound, "no price for asset %q yet", id)
	}
	if err != nil {
		logger.Log().Errorf("GetPrice(%s): %v", id, err)
		return nil, status.Errorf(codes.Internal, "fetch latest: %v", err)
	}
	return row.ToProto(), nil
}

// Subscribe streams AggregatedPrice updates for the requested asset ids.
//
// On stream open the server pushes the current persisted price for each
// subscribed asset (if any), then forwards every aggregator publish that
// matches the subscription filter. The stream closes when the client
// cancels, the bus subscription is cancelled, or the aggregator stops.
func (h *PriceServiceHandler) Subscribe(req *pricev1.SubscribeRequest, srv pricev1.PriceService_SubscribeServer) error {
	rawIDs := req.GetAssetIds()
	if len(rawIDs) == 0 {
		return status.Error(codes.InvalidArgument, "asset_ids must not be empty")
	}

	wanted := make(map[models.AssetID]struct{}, len(rawIDs))
	for _, raw := range rawIDs {
		id := models.AssetID(raw)
		if err := id.Validate(); err != nil {
			return status.Errorf(codes.InvalidArgument, "asset_id %q: %v", raw, err)
		}
		wanted[id] = struct{}{}
	}

	// Initial snapshot — best-effort, log + skip on per-asset error.
	for id := range wanted {
		row, err := h.repo.GetLatest(srv.Context(), id)
		if errors.Is(err, models.ErrAssetNotTracked) {
			continue
		}
		if err != nil {
			logger.Log().Warnf("Subscribe(%s): initial snapshot: %v", id, err)
			continue
		}
		if err := srv.Send(row.ToProto()); err != nil {
			return fmt.Errorf("Subscribe: send initial: %w", err)
		}
	}

	// Live tail.
	ch, cancel := h.bus.Subscribe()
	defer cancel()

	ctx := srv.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case agg, ok := <-ch:
			if !ok {
				return nil // bus closed; treat as graceful end-of-stream.
			}
			if _, want := wanted[agg.AssetID]; !want {
				continue
			}
			if err := srv.Send(agg.ToProto()); err != nil {
				return fmt.Errorf("Subscribe: send update for %s: %w", agg.AssetID, err)
			}
		}
	}
}
