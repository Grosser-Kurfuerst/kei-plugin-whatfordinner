package decision

import (
	"context"
	"sync"
	"time"

	"example.com/whatfordinner/internal/catalog"
)

// Service 串行处理一份插件实例的个人状态操作。
type Service struct {
	mu          sync.Mutex
	catalog     *catalog.Catalog
	store       Store
	now         func() time.Time
	randomFloat func() float64
	location    *time.Location
}

// New 构建决策服务，生产和测试分别提供真实或可控时钟及随机函数。
func New(c *catalog.Catalog, store Store, now func() time.Time, randomFloat func() float64) (*Service, error) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	return &Service{catalog: c, store: store, now: now, randomFloat: randomFloat, location: location}, nil
}

// Recommend 返回有效的当前结果，没有结果时才抽选。
func (s *Service) Recommend(ctx context.Context, scope Scope) (Recommendation, error) {
	return s.choose(ctx, scope, false)
}

// Reroll 排除本轮已经推荐的 ID 后抽选。
func (s *Service) Reroll(ctx context.Context, scope Scope) (Recommendation, error) {
	return s.choose(ctx, scope, true)
}

func (s *Service) load(ctx context.Context, scope Scope) (UserState, error) {
	if err := ctx.Err(); err != nil {
		return UserState{}, err
	}
	state, err := s.store.Load(ctx, scope)
	if err != nil {
		return state, err
	}
	today := s.now().In(s.location).Format(time.DateOnly)
	if state.Daily.Date != today {
		state.Daily = DailyDecision{Date: today}
	}
	return state, nil
}

func (s *Service) choose(ctx context.Context, scope Scope, reroll bool) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load(ctx, scope)
	if err != nil {
		return Recommendation{}, err
	}
	if !reroll && state.Daily.Current != nil {
		option, ok := s.catalog.Find(state.Daily.Current.ID)
		if !ok || !option.Enabled {
			return Recommendation{}, ErrCurrentUnavailable
		}
		return Recommendation{Option: snapshot(option)}, nil
	}
	tried := make(map[string]bool, len(state.Daily.TriedIDs))
	for _, id := range state.Daily.TriedIDs {
		tried[id] = true
	}
	var available []WeightedOption
	eligible := 0
	for _, option := range s.catalog.List() {
		if !option.Enabled {
			continue
		}
		eligible++
		if !tried[option.ID] {
			available = append(available, WeightedOption{Option: option, Weight: 1})
		}
	}
	if eligible == 0 {
		return Recommendation{}, ErrNoCandidates
	}
	if len(available) == 0 {
		return Recommendation{}, ErrRoundExhausted
	}
	selected, err := Pick(available, s.randomFloat())
	if err != nil {
		return Recommendation{}, err
	}
	current := snapshot(selected)
	state.Daily.Current = &current
	state.Daily.TriedIDs = append(state.Daily.TriedIDs, selected.ID)
	if err := s.store.Save(ctx, scope, state); err != nil {
		return Recommendation{}, err
	}
	return Recommendation{Option: current}, nil
}

func snapshot(option catalog.Option) OptionSnapshot {
	return OptionSnapshot{ID: option.ID, Name: option.Name, Venue: option.Venue, PriceCents: option.PriceCents}
}
