package decision

import (
	"example.com/whatfordinner/internal/catalog"
	"math"
	"testing"
)

func TestPickIntervals(t *testing.T) {
	items := []WeightedOption{{Option: catalog.Option{ID: "a"}, Weight: 1}, {Option: catalog.Option{ID: "b"}, Weight: 1}}
	for _, tc := range []struct {
		random float64
		id     string
	}{{0, "a"}, {0.499, "a"}, {0.5, "b"}, {math.Nextafter(1, 0), "b"}} {
		got, err := Pick(items, tc.random)
		if err != nil || got.ID != tc.id {
			t.Fatalf("random=%v got=%+v err=%v", tc.random, got, err)
		}
	}
	items[1].Weight = 1.0 / 8
	for _, tc := range []struct {
		random float64
		id     string
	}{{0.8, "a"}, {8.0 / 9, "b"}, {0.99, "b"}} {
		got, err := Pick(items, tc.random)
		if err != nil || got.ID != tc.id {
			t.Fatalf("random=%v got=%+v err=%v", tc.random, got, err)
		}
	}
}
