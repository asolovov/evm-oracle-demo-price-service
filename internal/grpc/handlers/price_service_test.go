package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/aggregator"
	pricev1 "github.com/asolovov/evm-oracle-demo-price-service/internal/genproto/price/v1"
	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// fakeSubscribeStream satisfies pricev1.PriceService_SubscribeServer enough
// for handler tests. Captures every Send via the sends channel; the
// embedded context is the one the handler will observe via srv.Context().
type fakeSubscribeStream struct {
	ctx   context.Context
	mu    sync.Mutex
	sends []*pricev1.AggregatedPrice
	sendC chan struct{}
}

func newFakeStream(ctx context.Context) *fakeSubscribeStream {
	return &fakeSubscribeStream{
		ctx:   ctx,
		sendC: make(chan struct{}, 64),
	}
}

func (f *fakeSubscribeStream) Send(p *pricev1.AggregatedPrice) error {
	f.mu.Lock()
	f.sends = append(f.sends, p)
	f.mu.Unlock()
	// Non-blocking signal; tests poll via Sends() or wait on the channel.
	select {
	case f.sendC <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeSubscribeStream) Sends() []*pricev1.AggregatedPrice {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*pricev1.AggregatedPrice, len(f.sends))
	copy(out, f.sends)
	return out
}

// ServerStream methods. We only need Context to be real.
func (f *fakeSubscribeStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeSubscribeStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeSubscribeStream) SetTrailer(metadata.MD)       {}
func (f *fakeSubscribeStream) Context() context.Context     { return f.ctx }
func (f *fakeSubscribeStream) SendMsg(any) error            { return nil }
func (f *fakeSubscribeStream) RecvMsg(any) error            { return nil }

// -- GetPrice --

func TestGetPriceHappyPath(t *testing.T) {
	repo := newMockRepo()
	repo.Seed("weth", 3450.0)
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	resp, err := h.GetPrice(context.Background(), &pricev1.GetPriceRequest{AssetId: "weth"})
	if err != nil {
		t.Fatalf("GetPrice: %v", err)
	}
	if resp.GetAssetId() != "weth" {
		t.Fatalf("AssetId = %q, want weth", resp.GetAssetId())
	}
	if resp.GetMedianPrice() != 3450.0 {
		t.Fatalf("MedianPrice = %v, want 3450", resp.GetMedianPrice())
	}
}

func TestGetPriceNotFound(t *testing.T) {
	repo := newMockRepo()
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	_, err := h.GetPrice(context.Background(), &pricev1.GetPriceRequest{AssetId: "weth"})
	if err == nil {
		t.Fatalf("expected NOT_FOUND, got nil")
	}
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.NotFound {
		t.Fatalf("expected NotFound status, got %v", err)
	}
}

func TestGetPriceInvalidArgument(t *testing.T) {
	repo := newMockRepo()
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	cases := []string{"", "WETH", "weth and bacon"}
	for _, c := range cases {
		t.Run("id="+c, func(t *testing.T) {
			_, err := h.GetPrice(context.Background(), &pricev1.GetPriceRequest{AssetId: c})
			s, ok := status.FromError(err)
			if !ok || s.Code() != codes.InvalidArgument {
				t.Fatalf("expected InvalidArgument, got %v", err)
			}
		})
	}
}

func TestGetPriceInternalOnRepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("simulated db down")
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	_, err := h.GetPrice(context.Background(), &pricev1.GetPriceRequest{AssetId: "weth"})
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.Internal {
		t.Fatalf("expected Internal status, got %v", err)
	}
}

// -- Subscribe --

func TestSubscribeRejectsEmptyAssetIds(t *testing.T) {
	repo := newMockRepo()
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	stream := newFakeStream(context.Background())
	err := h.Subscribe(&pricev1.SubscribeRequest{}, stream)
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestSubscribeRejectsBadAssetId(t *testing.T) {
	repo := newMockRepo()
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	stream := newFakeStream(context.Background())
	err := h.Subscribe(&pricev1.SubscribeRequest{AssetIds: []string{"WETH"}}, stream)
	s, ok := status.FromError(err)
	if !ok || s.Code() != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestSubscribeSendsInitialSnapshotAndLiveTail(t *testing.T) {
	repo := newMockRepo()
	repo.Seed("weth", 3450.0)
	bus := aggregator.NewBus(4)
	h := NewPriceServiceHandler(bus, repo)

	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeStream(ctx)

	done := make(chan error, 1)
	go func() {
		done <- h.Subscribe(&pricev1.SubscribeRequest{AssetIds: []string{"weth"}}, stream)
	}()

	// 1. Initial snapshot fires synchronously inside the goroutine; wait for
	//    it to be observable.
	if !waitForSends(stream, 1, 200*time.Millisecond) {
		t.Fatalf("did not receive initial snapshot within 200ms")
	}
	first := stream.Sends()[0]
	if first.GetMedianPrice() != 3450.0 {
		t.Fatalf("initial snapshot price = %v, want 3450", first.GetMedianPrice())
	}

	// 2. Publish a live update; subscriber should forward it.
	bus.Publish(models.AggregatedPrice{
		AssetID:      "weth",
		MedianPrice:  3460.0,
		AggregatedAt: time.Now().UTC(),
	})
	if !waitForSends(stream, 2, 200*time.Millisecond) {
		t.Fatalf("did not receive live update within 200ms")
	}
	second := stream.Sends()[1]
	if second.GetMedianPrice() != 3460.0 {
		t.Fatalf("live update price = %v, want 3460", second.GetMedianPrice())
	}

	// 3. Cancel the stream; handler must return ctx.Err().
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler returned %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("handler did not return after context cancel")
	}
}

func TestSubscribeFiltersByAssetID(t *testing.T) {
	repo := newMockRepo() // empty — no initial snapshot fires.
	bus := aggregator.NewBus(8)
	h := NewPriceServiceHandler(bus, repo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := newFakeStream(ctx)

	go func() { _ = h.Subscribe(&pricev1.SubscribeRequest{AssetIds: []string{"weth"}}, stream) }()

	// Wait briefly for Subscribe to register on the bus before publishing.
	deadline := time.Now().Add(200 * time.Millisecond)
	for bus.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// wbtc publish: not in the filter; subscriber must skip it.
	bus.Publish(models.AggregatedPrice{AssetID: "wbtc", MedianPrice: 60000})
	// weth publish: in the filter; should be forwarded.
	bus.Publish(models.AggregatedPrice{AssetID: "weth", MedianPrice: 3450})

	if !waitForSends(stream, 1, 200*time.Millisecond) {
		t.Fatalf("expected one matching send, got %d", len(stream.Sends()))
	}
	got := stream.Sends()
	if len(got) != 1 || got[0].GetAssetId() != "weth" {
		t.Fatalf("got %+v, want one send for weth", got)
	}
}

// waitForSends polls stream.Sends() until length >= want or timeout.
func waitForSends(s *fakeSubscribeStream, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(s.Sends()) >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return len(s.Sends()) >= want
}
