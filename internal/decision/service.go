package decision

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
	// 嵌入时区库，避免精简镜像缺少 zoneinfo 时无法启动。
	_ "time/tzdata"

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

// SetCatalog 替换当前清单；调用方必须传入已校验的 Catalog。
func (s *Service) SetCatalog(c *catalog.Catalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog = c
}

// ListCatalog 返回清单副本，包含停用项。
func (s *Service) ListCatalog() []catalog.Option {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog.List()
}

// FindCatalogOption 按 id 返回副本。
func (s *Service) FindCatalogOption(id string) (catalog.Option, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog.Find(id)
}

// HasCatalogTag 报告清单中是否有任一选项使用该标签。
func (s *Service) HasCatalogTag(tag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.catalog.HasTag(tag)
}

// RecommendFor 在当前会话的筛选条件下生成个人推荐，历史仍归属于个人。
func (s *Service) RecommendFor(ctx context.Context, scope Scope, profile Profile) (Recommendation, error) {
	return s.choose(ctx, scope, false, profile)
}

// RerollFor 在当前会话的筛选条件下换选个人推荐。
func (s *Service) RerollFor(ctx context.Context, scope Scope, profile Profile) (Recommendation, error) {
	return s.choose(ctx, scope, true, profile)
}

func day(now time.Time) string { return now.Format(time.DateOnly) }

func (s *Service) load(ctx context.Context, scope Scope, now time.Time) (UserState, error) {
	if err := ctx.Err(); err != nil {
		return UserState{}, err
	}
	state, err := s.store.Load(ctx, scope)
	if err != nil {
		return state, err
	}
	today := day(now)
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

func (s *Service) choose(ctx context.Context, scope Scope, reroll bool, profile Profile) (Recommendation, error) {
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
	if state.Profile.BudgetCents != profile.BudgetCents || !slices.Equal(state.Profile.ExcludedTags, profile.ExcludedTags) {
		state.Profile = Profile{BudgetCents: profile.BudgetCents, ExcludedTags: slices.Clone(profile.ExcludedTags)}
		state.Daily.Current = nil
		state.Daily.TriedIDs = nil
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

// MarkEaten 将当前个人推荐和已吃状态一次保存，重复调用不追加记录。
// 校验使用生成当前推荐时的条件快照，避免会话设置变化后拒绝刚生成的推荐。
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
	return s.markEaten(ctx, scope, state, *state.Daily.Current, state.Profile, now)
}

// MarkEatenOption 将指定选项记录为实际用餐；选项按调用方给出的会话条件校验并保存为个人快照。
func (s *Service) MarkEatenOption(ctx context.Context, scope Scope, option OptionSnapshot, profile Profile) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return Recommendation{}, err
	}
	if state.Daily.Eaten {
		if state.Daily.Current != nil && state.Daily.Current.ID == option.ID {
			return makeResult(*state.Daily.Current, state.History, now, true), nil
		}
		return Recommendation{}, ErrAlreadyEaten
	}
	return s.markEaten(ctx, scope, state, option, profile, now)
}

func (s *Service) markEaten(ctx context.Context, scope Scope, state UserState, option OptionSnapshot, profile Profile, now time.Time) (Recommendation, error) {
	catalogOption, ok := s.catalog.Find(option.ID)
	if !ok || !matches(catalogOption, profile) {
		return Recommendation{}, ErrCurrentUnavailable
	}
	current := snapshot(catalogOption)
	state.Profile = Profile{BudgetCents: profile.BudgetCents, ExcludedTags: slices.Clone(profile.ExcludedTags)}
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

// MaxPollOptions 是每轮投票列表的最大候选数。
const MaxPollOptions = 5

// suggest 使用发起人的个人用餐历史为新列表抽选至多 MaxPollOptions 项，不写个人状态。
func (s *Service) suggest(ctx context.Context, scope Scope, profile Profile) ([]OptionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().In(s.location)
	state, err := s.load(ctx, scope, now)
	if err != nil {
		return nil, err
	}
	return s.suggestWithHistory(profile, state.History, now)
}

// suggestShared 使用会话共享的已吃历史为新列表抽选，不写个人状态。
func (s *Service) suggestShared(ctx context.Context, profile Profile, history []MealRecord) ([]OptionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.suggestWithHistory(profile, history, s.now().In(s.location))
}

func (s *Service) suggestWithHistory(profile Profile, history []MealRecord, now time.Time) ([]OptionSnapshot, error) {
	items := s.candidates(profile, history, now)
	var result []OptionSnapshot
	for len(items) > 0 && len(result) < MaxPollOptions {
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
