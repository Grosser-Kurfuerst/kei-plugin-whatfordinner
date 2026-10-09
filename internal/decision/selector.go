package decision

import (
	"fmt"
	"math"

	"example.com/whatfordinner/internal/catalog"
)

// WeightedOption 是参与本次抽选的候选及正权重。
type WeightedOption struct {
	Option catalog.Option
	Weight float64
}

// Pick 用 [0,1) 的随机值在累计权重区间中选择候选。
func Pick(items []WeightedOption, random float64) (catalog.Option, error) {
	if len(items) == 0 {
		return catalog.Option{}, ErrNoCandidates
	}
	if random < 0 || random >= 1 || math.IsNaN(random) {
		return catalog.Option{}, fmt.Errorf("random value must be in [0,1)")
	}
	total := 0.0
	for _, item := range items {
		if item.Weight <= 0 || math.IsNaN(item.Weight) || math.IsInf(item.Weight, 0) {
			return catalog.Option{}, fmt.Errorf("weight must be positive and finite")
		}
		total += item.Weight
	}
	ticket, cumulative := random*total, 0.0
	for _, item := range items {
		cumulative += item.Weight
		if ticket < cumulative {
			return item.Option, nil
		}
	}
	// 当 random 接近 1 时乘法可能舍入到 total，归入最后一个区间。
	return items[len(items)-1].Option, nil
}
