package aggregator

import (
	"testing"
	"time"

	"github.com/asolovov/evm-oracle-demo-price-service/internal/models"
)

func TestBusBufferDefault(t *testing.T) {
	b := NewBus(0)
	if b.buffer != 16 {
		t.Fatalf("buffer default = %d, want 16", b.buffer)
	}
	b = NewBus(-5)
	if b.buffer != 16 {
		t.Fatalf("negative buffer should default to 16, got %d", b.buffer)
	}
	b = NewBus(32)
	if b.buffer != 32 {
		t.Fatalf("buffer = %d, want 32", b.buffer)
	}
}

func TestBusPublishToManySubscribers(t *testing.T) {
	b := NewBus(2)
	subA, cancelA := b.Subscribe()
	defer cancelA()
	subB, cancelB := b.Subscribe()
	defer cancelB()

	if b.Count() != 2 {
		t.Fatalf("Count = %d, want 2", b.Count())
	}

	want := models.AggregatedPrice{AssetID: "weth", MedianPrice: 3450}
	if dropped := b.Publish(want); dropped != 0 {
		t.Fatalf("Publish dropped = %d, want 0", dropped)
	}

	for i, ch := range []<-chan models.AggregatedPrice{subA, subB} {
		select {
		case got := <-ch:
			if got.MedianPrice != want.MedianPrice {
				t.Fatalf("sub %d got %v, want %v", i, got, want)
			}
		case <-time.After(50 * time.Millisecond):
			t.Fatalf("sub %d did not receive", i)
		}
	}
}

func TestBusDropsOnFullSubscriber(t *testing.T) {
	b := NewBus(1) // tiny buffer

	_, cancel := b.Subscribe()
	defer cancel()

	// First message fills the buffer; second + third get dropped on this sub.
	b.Publish(models.AggregatedPrice{AssetID: "weth", MedianPrice: 1})
	dropped := b.Publish(models.AggregatedPrice{AssetID: "weth", MedianPrice: 2})
	dropped += b.Publish(models.AggregatedPrice{AssetID: "weth", MedianPrice: 3})
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
}

func TestBusCancelDeregistersAndClosesChan(t *testing.T) {
	b := NewBus(4)
	sub, cancel := b.Subscribe()
	if b.Count() != 1 {
		t.Fatalf("Count = %d, want 1 after subscribe", b.Count())
	}
	cancel()
	if b.Count() != 0 {
		t.Fatalf("Count = %d, want 0 after cancel", b.Count())
	}
	// Channel should be closed.
	if _, ok := <-sub; ok {
		t.Fatalf("subscription channel should be closed after cancel")
	}
	// Calling cancel a second time must be a no-op (no double close panic).
	cancel()
}
