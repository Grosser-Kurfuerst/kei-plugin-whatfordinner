package decision

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// PollScope 标识群共享列表或私聊个人列表，与个人历史的 Scope 分开。
type PollScope struct {
	Platform string `json:"platform"`
	BotID    string `json:"bot_id"`
	Kind     string `json:"kind"`
	ID       string `json:"id"`
}

// PollScope.Kind 的取值与框架消息类型一致。
const (
	PollKindPrivate = "private"
	PollKindGroup   = "group"
	PollKindChannel = "channel"
)

// Shared 报告该作用域是否在成员之间共享。
func (s PollScope) Shared() bool {
	return s.Kind == PollKindGroup || s.Kind == PollKindChannel
}

// PollState 保存当天列表、每个用户的当前选票和共享已吃历史；票数由选票汇总。
type PollState struct {
	Date    string            `json:"date"`
	Profile Profile           `json:"profile"`
	Options []OptionSnapshot  `json:"options"`
	Ballots map[string]string `json:"ballots"`
	// Meals 是共享会话的实际用餐历史，跨日保留，用于生成下一轮列表。
	Meals []SharedMeal `json:"meals,omitempty"`
}

// PollStore 通过一次保存持久化整个列表和所有选票。
type PollStore interface {
	LoadPoll(context.Context, PollScope) (PollState, error)
	SavePoll(context.Context, PollScope, PollState) error
}

type PollEntry struct {
	Option    OptionSnapshot
	Votes     int
	Available bool
}

type PollView struct {
	Date    string
	Profile Profile
	Entries []PollEntry
	Voters  int
}

var (
	ErrNoPoll                = errors.New("今天还没有投票列表，请先发送 /dinner 或 /dinner 25。")
	ErrInvalidOption         = errors.New("id 不在当前列表中，请发送 /dinner 查看 id。")
	ErrPollOptionUnavailable = errors.New("该选项当前不可用，请发送 /dinner 查看其他可用选项。")
	ErrUnknownTag            = errors.New("标签不在当前词表中，请发送 /dinner 标签 查看支持的标签")
)

// PollService 串行处理共享列表的创建和投票，个人服务只提供清单与抽选规则。
type PollService struct {
	mu       sync.Mutex
	personal *Service
	store    PollStore
}

func NewPollService(personal *Service, store PollStore) *PollService {
	return &PollService{personal: personal, store: store}
}

func (s *PollService) load(ctx context.Context, scope PollScope) (PollState, bool, error) {
	if err := ctx.Err(); err != nil {
		return PollState{}, false, err
	}
	state, err := s.store.LoadPoll(ctx, scope)
	if err != nil {
		return state, false, err
	}
	now := s.personal.now().In(s.personal.location)
	today := day(now)
	changed := state.Date != today
	if changed {
		state.Date = today
		state.Options = nil
		state.Ballots = nil
	}
	state.Meals = pruneSharedMeals(state.Meals, now)
	return state, changed, nil
}

func pruneSharedMeals(meals []SharedMeal, now time.Time) []SharedMeal {
	oldest := day(now.AddDate(0, 0, -29))
	today := day(now)
	retained := meals[:0]
	for _, meal := range meals {
		if meal.Date >= oldest && meal.Date <= today {
			retained = append(retained, meal)
		}
	}
	return retained
}

// List 查询或创建当天列表。同预算保留列表与票数，改变预算开始新一轮。
func (s *PollService) List(ctx context.Context, scope PollScope, initiator Scope, budget *int64) (PollView, error) {
	if budget != nil && *budget < 0 {
		return PollView{}, fmt.Errorf("预算不能为负")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, changed, err := s.load(ctx, scope)
	if err != nil {
		return PollView{}, err
	}
	if budget != nil && state.Profile.BudgetCents != *budget {
		state.Profile.BudgetCents = *budget
		state.Options = nil
		state.Ballots = nil
		changed = true
	}
	if len(state.Options) == 0 {
		if scope.Shared() {
			state.Options, err = s.personal.suggestShared(ctx, state.Profile, sharedMealRecords(state.Meals))
		} else {
			state.Options, err = s.personal.suggest(ctx, initiator, state.Profile)
		}
		if err != nil {
			return PollView{}, err
		}
		if len(state.Options) > 0 {
			changed = true
		}
	}
	if changed {
		if err := s.store.SavePoll(ctx, scope, state); err != nil {
			return PollView{}, err
		}
	}
	if len(state.Options) == 0 {
		return PollView{}, ErrNoCandidates
	}
	return s.view(state), nil
}

// RecommendFor 在会话锁内读取当前条件并生成个人推荐，避免条件读取与抽选之间被其他成员修改。
func (s *PollService) RecommendFor(ctx context.Context, pollScope PollScope, scope Scope) (Recommendation, error) {
	return s.recommend(ctx, pollScope, scope, false)
}

// RerollFor 在会话锁内读取当前条件并换选个人推荐。
func (s *PollService) RerollFor(ctx context.Context, pollScope PollScope, scope Scope) (Recommendation, error) {
	return s.recommend(ctx, pollScope, scope, true)
}

func (s *PollService) recommend(ctx context.Context, pollScope PollScope, scope Scope, reroll bool) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx, pollScope)
	if err != nil {
		return Recommendation{}, err
	}
	if reroll {
		return s.personal.RerollFor(ctx, scope, state.Profile)
	}
	return s.personal.RecommendFor(ctx, scope, state.Profile)
}

func optionByID(options []OptionSnapshot, id string) (OptionSnapshot, bool) {
	for _, option := range options {
		if option.ID == id {
			return option, true
		}
	}
	return OptionSnapshot{}, false
}

// Vote 每人一票，按稳定 id 选投；重复投同一 id 不写入，改投直接替换原票。
func (s *PollService) Vote(ctx context.Context, scope PollScope, voter, id string) (PollView, error) {
	if strings.TrimSpace(voter) == "" {
		return PollView{}, fmt.Errorf("投票者不能为空")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PollView{}, ErrInvalidOption
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx, scope)
	if err != nil {
		return PollView{}, err
	}
	if len(state.Options) == 0 {
		return PollView{}, ErrNoPoll
	}
	if _, ok := optionByID(state.Options, id); !ok {
		return PollView{}, ErrInvalidOption
	}
	option, ok := s.personal.catalog.Find(id)
	if !ok || !matches(option, state.Profile) {
		return PollView{}, ErrPollOptionUnavailable
	}
	if state.Ballots[voter] == id {
		return s.view(state), nil
	}
	if state.Ballots == nil {
		state.Ballots = make(map[string]string)
	}
	state.Ballots[voter] = id
	if err := s.store.SavePoll(ctx, scope, state); err != nil {
		return PollView{}, err
	}
	return s.view(state), nil
}

// MarkEaten 记录实际用餐。id 为空时记录当前个人推荐；否则记录当前会话列表中对应的 id。
// 共享会话会把结果同时写入会话共享历史，供下一次列表加权使用。
func (s *PollService) MarkEaten(ctx context.Context, pollScope PollScope, scope Scope, id *string) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx, pollScope)
	if err != nil {
		return Recommendation{}, err
	}
	var result Recommendation
	if id == nil {
		result, err = s.personal.MarkEaten(ctx, scope)
	} else {
		if len(state.Options) == 0 {
			return Recommendation{}, ErrNoPoll
		}
		option, ok := optionByID(state.Options, strings.TrimSpace(*id))
		if !ok {
			return Recommendation{}, ErrInvalidOption
		}
		catalogOption, found := s.personal.catalog.Find(option.ID)
		if !found || !matches(catalogOption, state.Profile) {
			return Recommendation{}, ErrPollOptionUnavailable
		}
		result, err = s.personal.MarkEatenOption(ctx, scope, option, state.Profile)
	}
	if err != nil {
		return Recommendation{}, err
	}
	if pollScope.Shared() {
		state.Meals = appendSharedMeal(state.Meals, SharedMeal{
			UserID: scope.UserID,
			MealRecord: MealRecord{
				Date:    state.Date,
				Option:  result.Option,
				EatenAt: s.personal.now().UTC(),
			},
		})
		if err := s.store.SavePoll(ctx, pollScope, state); err != nil {
			return Recommendation{}, err
		}
	}
	return result, nil
}

func appendSharedMeal(meals []SharedMeal, meal SharedMeal) []SharedMeal {
	for _, existing := range meals {
		if existing.UserID == meal.UserID && existing.Date == meal.Date && existing.Option.ID == meal.Option.ID {
			return meals
		}
	}
	return append(meals, meal)
}

func sharedMealRecords(meals []SharedMeal) []MealRecord {
	records := make([]MealRecord, len(meals))
	for i, meal := range meals {
		records[i] = meal.MealRecord
	}
	return records
}

// Profile 返回当前会话的预算和忌口，跨日保留设置。
func (s *PollService) Profile(ctx context.Context, scope PollScope) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx, scope)
	if err != nil {
		return Profile{}, err
	}
	return state.Profile, nil
}

// SetExcludedTags 设置会话忌口，条件改变时清空本轮列表和选票。
func (s *PollService) SetExcludedTags(ctx context.Context, scope PollScope, tags []string) (Profile, error) {
	unique, err := uniqueTags(tags)
	if err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.load(ctx, scope)
	if err != nil {
		return Profile{}, err
	}
	if !slices.Equal(state.Profile.ExcludedTags, unique) {
		state.Profile.ExcludedTags = unique
		state.Options = nil
		state.Ballots = nil
	}
	if err := s.store.SavePoll(ctx, scope, state); err != nil {
		return Profile{}, err
	}
	return state.Profile, nil
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

func (s *PollService) view(state PollState) PollView {
	counts := make(map[string]int)
	for _, id := range state.Ballots {
		counts[id]++
	}
	view := PollView{Date: state.Date, Profile: state.Profile, Voters: len(state.Ballots)}
	for _, saved := range state.Options {
		option, found := s.personal.catalog.Find(saved.ID)
		entry := PollEntry{Option: saved, Votes: counts[saved.ID], Available: found && matches(option, state.Profile)}
		if found {
			entry.Option = snapshot(option)
		}
		view.Entries = append(view.Entries, entry)
	}
	return view
}
