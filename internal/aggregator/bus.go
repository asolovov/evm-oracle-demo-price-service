package aggregator

import (
	"sync"

	"github.com/google/uuid"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

// Bus is an in-memory pub/sub for aggregated prices.
//
// The gRPC Subscribe handler registers one subscription per client and
// drains its channel; the aggregator publishes a message after every
// successful tick. Subscribers filter on AssetID themselves — the bus does
// not partition by asset (subscribers typically follow a small handful, and
// the cost of receive-and-drop is negligible at our throughput).
//
// Bus is safe for concurrent Publish + Subscribe; Publish never blocks on a
// slow subscriber — full channels drop the message for that one subscriber
// only (logged, never propagated). The trade-off keeps a stuck WebSocket
// client from stalling the aggregator's hot path.
type Bus struct {
	mu      sync.RWMutex
	subs    map[uuid.UUID]chan models.AggregatedPrice
	buffer  int
}

// NewBus returns a Bus where each subscription buffers up to `buffer`
// messages before slow subscribers start dropping.
func NewBus(buffer int) *Bus {
	if buffer < 1 {
		buffer = 16
	}
	return &Bus{
		subs:   make(map[uuid.UUID]chan models.AggregatedPrice),
		buffer: buffer,
	}
}

// Subscribe registers a new subscription. The returned receive-only channel
// emits every aggregated price; the cancel function deregisters the
// subscription and closes the channel. Callers MUST invoke cancel on stream
// teardown (defer cancel() in the gRPC handler).
func (b *Bus) Subscribe() (<-chan models.AggregatedPrice, func()) {
	ch := make(chan models.AggregatedPrice, b.buffer)
	id := uuid.New()

	b.mu.Lock()
	b.subs[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

// Publish pushes the price to every subscriber. Returns the count of
// subscribers that dropped the message because their buffer was full;
// callers can use this for metrics (price_subscriber_drops_total).
func (b *Bus) Publish(price models.AggregatedPrice) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	dropped := 0
	for _, ch := range b.subs {
		select {
		case ch <- price:
		default:
			dropped++
		}
	}
	return dropped
}

// Count returns the live subscription count. Useful for /metrics.
func (b *Bus) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
