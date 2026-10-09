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

// PollState 保持本轮编号顺序，仅存每个用户的当前选票，票数由选票汇总。
type PollState struct {
	Date    string            `json:"date"`
	Profile Profile           `json:"profile"`
	Options []OptionSnapshot  `json:"options"`
	Ballots map[string]string `json:"ballots"`
}

// PollStore 通过一次保存持久化整个列表和所有选票。
type PollStore interface {
	LoadPoll(context.Context, PollScope) (PollState, error)
	SavePoll(context.Context, PollScope, PollState) error
}

type PollEntry struct {
	Number    int
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
	ErrInvalidVote           = errors.New("编号不在当前列表中，请发送 /dinner 查看编号。")
	ErrPollOptionUnavailable = errors.New("该选项当前不可投票，请发送 /dinner 查看其他可用选项。")
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
	today := s.personal.now().In(s.personal.location).Format(time.DateOnly)
	changed := state.Date != today
	if changed {
		state.Date = today
		state.Options = nil
		state.Ballots = nil
	}
	return state, changed, nil
}

// List 查询或创建当天列表。同预算保留编号与票数，改变预算开始新一轮。
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
		state.Options, err = s.personal.suggest(ctx, initiator, state.Profile)
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

// Vote 每人一票，重复投同一项不写入，改投直接替换原票。
func (s *PollService) Vote(ctx context.Context, scope PollScope, voter string, number int) (PollView, error) {
	if strings.TrimSpace(voter) == "" {
		return PollView{}, fmt.Errorf("投票者不能为空")
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
	if number < 1 || number > len(state.Options) {
		return PollView{}, ErrInvalidVote
	}
	id := state.Options[number-1].ID
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

func (s *PollService) view(state PollState) PollView {
	counts := make(map[string]int)
	for _, id := range state.Ballots {
		counts[id]++
	}
	view := PollView{Date: state.Date, Profile: state.Profile, Voters: len(state.Ballots)}
	for i, saved := range state.Options {
		option, found := s.personal.catalog.Find(saved.ID)
		entry := PollEntry{Number: i + 1, Option: saved, Votes: counts[saved.ID], Available: found && matches(option, state.Profile)}
		if found {
			entry.Option = snapshot(option)
		}
		view.Entries = append(view.Entries, entry)
	}
	return view
}
