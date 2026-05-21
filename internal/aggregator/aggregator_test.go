package aggregator

import (
	"math"
	"testing"
)

func TestComputeMedian(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"single", []float64{42.5}, 42.5},
		{"odd", []float64{1, 2, 3}, 2},
		{"even", []float64{1, 2, 3, 4}, 2.5},
		{"out-of-order", []float64{3, 1, 2}, 2},
		{"identical", []float64{5, 5, 5}, 5},
		{"negative-and-positive", []float64{-1, 0, 1}, 0},
		{"large-spread", []float64{1, 1, 100}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeMedian(c.in)
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("computeMedian(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestComputeMedianDoesNotMutateInput(t *testing.T) {
	in := []float64{3, 1, 2}
	_ = computeMedian(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Fatalf("computeMedian mutated input: %v", in)
	}
}
