package decision

import (
	"context"
	"sort"
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

func (s *Service) load(ctx context.Context, scope Scope, now time.Time) (UserState, error) {
	if err := ctx.Err(); err != nil {
		return UserState{}, err
	}
	state, err := s.store.Load(ctx, scope)
	if err != nil {
		return state, err
	}
	today := now.Format(time.DateOnly)
	if state.Daily.Date != today {
		state.Daily = DailyDecision{Date: today}
	}
	oldest := now.AddDate(0, 0, -29).Format(time.DateOnly)
	retained := make([]MealRecord, 0, len(state.History))
	for _, record := range state.History {
		if record.Date >= oldest && record.Date <= today {
			retained = append(retained, record)
		}
	}
	state.History = retained
	return state, nil
}

func (s *Service) choose(ctx context.Context, scope Scope, reroll bool) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return Recommendation{}, err
	}
	if state.Daily.Eaten {
		if reroll {
			return Recommendation{}, ErrAlreadyEaten
		}
		return makeResult(*state.Daily.Current, state.History, now, true), nil
	}
	if !reroll && state.Daily.Current != nil {
		option, ok := s.catalog.Find(state.Daily.Current.ID)
		if !ok || !option.Enabled {
			return Recommendation{}, ErrCurrentUnavailable
		}
		return makeResult(snapshot(option), state.History, now, false), nil
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
			weight, _ := Weight(option.ID, state.History, now)
			available = append(available, WeightedOption{Option: option, Weight: weight})
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
	return makeResult(current, state.History, now, false), nil
}

// MarkEaten 将当前选项和已吃状态一次保存，重复调用不追加记录。
func (s *Service) MarkEaten(ctx context.Context, scope Scope) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return Recommendation{}, err
	}
	if state.Daily.Current == nil {
		return Recommendation{}, ErrNoCurrent
	}
	if state.Daily.Eaten {
		return makeResult(*state.Daily.Current, state.History, now, true), nil
	}
	option, ok := s.catalog.Find(state.Daily.Current.ID)
	if !ok || !option.Enabled {
		return Recommendation{}, ErrCurrentUnavailable
	}
	current := snapshot(option)
	state.Daily.Current = &current
	state.Daily.Eaten = true
	state.History = append(state.History, MealRecord{Date: state.Daily.Date, Option: current, EatenAt: now.UTC()})
	if err := s.store.Save(ctx, scope, state); err != nil {
		return Recommendation{}, err
	}
	return makeResult(current, state.History, now, true), nil
}

// History 返回三十天窗口内最近七条记录，按日期倒序排列。
func (s *Service) History(ctx context.Context, scope Scope) ([]MealRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load(ctx, scope, s.now().In(s.location))
	if err != nil {
		return nil, err
	}
	sort.Slice(state.History, func(i, j int) bool { return state.History[i].Date > state.History[j].Date })
	if len(state.History) > 7 {
		state.History = state.History[:7]
	}
	return state.History, nil
}

func makeResult(option OptionSnapshot, history []MealRecord, now time.Time, eaten bool) Recommendation {
	_, count := Weight(option.ID, history, now)
	return Recommendation{Option: option, Eaten: eaten, RecentMeals: count}
}

func snapshot(option catalog.Option) OptionSnapshot {
	return OptionSnapshot{ID: option.ID, Name: option.Name, Venue: option.Venue, PriceCents: option.PriceCents}
}
