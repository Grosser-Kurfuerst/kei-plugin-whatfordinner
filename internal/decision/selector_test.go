package decision

import (
	"example.com/whatfordinner/internal/catalog"
	"math"
	"testing"
	"time"
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

func TestSevenDayWeights(t *testing.T) {
	today := time.Date(2026, 10, 9, 21, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	for _, tc := range []struct {
		days   []int
		weight float64
		count  int
	}{{nil, 1, 0}, {[]int{1}, 1.0 / 8, 1}, {[]int{3}, 1.0 / 6, 1}, {[]int{7}, 0.5, 1}, {[]int{1, 3}, 1.0 / 13, 2}, {[]int{0, 8}, 1, 0}} {
		var history []MealRecord
		for _, d := range tc.days {
			history = append(history, MealRecord{Date: today.AddDate(0, 0, -d).Format(time.DateOnly), Option: OptionSnapshot{ID: "a"}})
		}
		history = append(history, MealRecord{Date: today.AddDate(0, 0, -1).Format(time.DateOnly), Option: OptionSnapshot{ID: "other"}})
		weight, count := Weight("a", history, today)
		if weight != tc.weight || count != tc.count {
			t.Fatalf("days=%v got=%v/%d want=%v/%d", tc.days, weight, count, tc.weight, tc.count)
		}
	}
}
