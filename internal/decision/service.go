package decision

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
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
	return s.choose(ctx, scope, false, nil)
}

// Reroll 排除本轮已经推荐的 ID 后抽选。
func (s *Service) Reroll(ctx context.Context, scope Scope) (Recommendation, error) {
	return s.choose(ctx, scope, true, nil)
}

// RecommendFor 在当前会话的筛选条件下生成个人推荐，历史仍归属于个人。
func (s *Service) RecommendFor(ctx context.Context, scope Scope, profile Profile) (Recommendation, error) {
	return s.choose(ctx, scope, false, &profile)
}

// RerollFor 在当前会话的筛选条件下换选个人推荐。
func (s *Service) RerollFor(ctx context.Context, scope Scope, profile Profile) (Recommendation, error) {
	return s.choose(ctx, scope, true, &profile)
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

func (s *Service) choose(ctx context.Context, scope Scope, reroll bool, profile *Profile) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return Recommendation{}, err
	}
	if profile != nil && (state.Profile.BudgetCents != profile.BudgetCents || !slices.Equal(state.Profile.ExcludedTags, profile.ExcludedTags)) {
		state.Profile = Profile{BudgetCents: profile.BudgetCents, ExcludedTags: slices.Clone(profile.ExcludedTags)}
		if !state.Daily.Eaten {
			state.Daily.Current = nil
			state.Daily.TriedIDs = nil
		}
	}
	if state.Daily.Eaten {
		if reroll {
			return Recommendation{}, ErrAlreadyEaten
		}
		return makeResult(*state.Daily.Current, state.History, now, true), nil
	}
	if !reroll && state.Daily.Current != nil {
		option, ok := s.catalog.Find(state.Daily.Current.ID)
		if !ok || !matches(option, state.Profile) {
			return Recommendation{}, ErrCurrentUnavailable
		}
		return makeResult(snapshot(option), state.History, now, false), nil
	}
	tried := make(map[string]bool, len(state.Daily.TriedIDs))
	for _, id := range state.Daily.TriedIDs {
		tried[id] = true
	}
	var available []WeightedOption
	eligible := s.candidates(state.Profile, state.History, now)
	for _, item := range eligible {
		if !tried[item.Option.ID] {
			available = append(available, item)
		}
	}
	if len(eligible) == 0 {
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
	if !ok || !matches(option, state.Profile) {
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

// Profile 查询当前预算和忌口，不改变状态。
func (s *Service) Profile(ctx context.Context, scope Scope) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load(ctx, scope, s.now().In(s.location))
	if err != nil {
		return Profile{}, err
	}
	return state.Profile, nil
}

// SetBudget 保存预算上限（分），0 表示不限。
func (s *Service) SetBudget(ctx context.Context, scope Scope, cents int64) (Profile, error) {
	if cents < 0 {
		return Profile{}, fmt.Errorf("预算不能为负")
	}
	return s.updateProfile(ctx, scope, func(p *Profile) { p.BudgetCents = cents })
}

// SetExcludedTags 替换忌口列表并去重，空列表表示无忌口。
func (s *Service) SetExcludedTags(ctx context.Context, scope Scope, tags []string) (Profile, error) {
	unique, err := uniqueTags(tags)
	if err != nil {
		return Profile{}, err
	}
	return s.updateProfile(ctx, scope, func(p *Profile) { p.ExcludedTags = unique })
}

func uniqueTags(tags []string) ([]string, error) {
	unique := make([]string, 0, len(tags))
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return nil, fmt.Errorf("忌口标签不能为空")
		}
		if !slices.Contains(unique, tag) {
			unique = append(unique, tag)
		}
	}
	return unique, nil
}

func (s *Service) candidates(profile Profile, history []MealRecord, now time.Time) []WeightedOption {
	var items []WeightedOption
	for _, option := range s.catalog.List() {
		if matches(option, profile) {
			weight, _ := Weight(option.ID, history, now)
			items = append(items, WeightedOption{Option: option, Weight: weight})
		}
	}
	return items
}

// suggest 为新投票列表抽选至多五项，使用发起人的实际用餐历史，不写个人状态。
func (s *Service) suggest(ctx context.Context, scope Scope, profile Profile) ([]OptionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	items := s.candidates(profile, state.History, now)
	var result []OptionSnapshot
	for len(items) > 0 && len(result) < 5 {
		selected, err := Pick(items, s.randomFloat())
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot(selected))
		for i, item := range items {
			if item.Option.ID == selected.ID {
				items = append(items[:i], items[i+1:]...)
				break
			}
		}
	}
	return result, nil
}

func (s *Service) updateProfile(ctx context.Context, scope Scope, update func(*Profile)) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.load(ctx, scope, s.now().In(s.location))
	if err != nil {
		return Profile{}, err
	}
	update(&state.Profile)
	if !state.Daily.Eaten {
		state.Daily.Current = nil
		state.Daily.TriedIDs = nil
	}
	if err := s.store.Save(ctx, scope, state); err != nil {
		return Profile{}, err
	}
	return state.Profile, nil
}

func matches(option catalog.Option, profile Profile) bool {
	if !option.Enabled {
		return false
	}
	if profile.BudgetCents > 0 && (option.PriceCents == nil || *option.PriceCents > profile.BudgetCents) {
		return false
	}
	for _, tag := range option.Tags {
		if slices.Contains(profile.ExcludedTags, tag) {
			return false
		}
	}
	return true
}

func makeResult(option OptionSnapshot, history []MealRecord, now time.Time, eaten bool) Recommendation {
	_, count := Weight(option.ID, history, now)
	return Recommendation{Option: option, Eaten: eaten, RecentMeals: count}
}

func snapshot(option catalog.Option) OptionSnapshot {
	return OptionSnapshot{ID: option.ID, Name: option.Name, Venue: option.Venue, PriceCents: option.PriceCents}
}
