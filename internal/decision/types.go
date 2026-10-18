// Package decision 实现个人晚饭推荐与会话投票的状态转换。
package decision

import (
	"context"
	"errors"
	"time"
)

// Scope 标识平台、机器人和调用者，不包含群 ID。
type Scope struct {
	Platform string `json:"platform"`
	BotID    string `json:"bot_id"`
	UserID   string `json:"user_id"`
}

// Profile 保存筛选条件；预算为 0 表示不限，忌口按标签精确匹配。
type Profile struct {
	BudgetCents  int64    `json:"budget_cents"`
	ExcludedTags []string `json:"excluded_tags"`
}

// OptionSnapshot 保存推荐或用餐时的展示数据。
type OptionSnapshot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Venue      string `json:"venue"`
	PriceCents *int64 `json:"price_cents"`
}

// DailyDecision 保存当天当前结果及本轮已经推荐的选项。
type DailyDecision struct {
	Date     string          `json:"date"`
	Current  *OptionSnapshot `json:"current"`
	TriedIDs []string        `json:"tried_ids"`
	Eaten    bool            `json:"eaten"`
}

// MealRecord 只记录明确标记的实际用餐，保留当时快照。
type MealRecord struct {
	Date    string         `json:"date"`
	Option  OptionSnapshot `json:"option"`
	EatenAt time.Time      `json:"eaten_at"`
}

// UserState 是一次读写的完整个人状态。
type UserState struct {
	// Profile 是最近一次生成个人推荐时使用的条件快照；会话当前设置保存在 PollState.Profile。
	Profile Profile       `json:"profile"`
	Daily   DailyDecision `json:"daily"`
	History []MealRecord  `json:"history"`
}

// Store 是决策服务需要的持久化边界。
type Store interface {
	Load(context.Context, Scope) (UserState, error)
	Save(context.Context, Scope, UserState) error
}

// Recommendation 是可以展示的决策结果。
type Recommendation struct {
	Option      OptionSnapshot
	Eaten       bool
	RecentMeals int
}

// 可预期的业务错误由插件展示给调用者。
var (
	ErrNoCandidates       = errors.New("暂无可选项，请检查候选清单、预算和忌口。")
	ErrRoundExhausted     = errors.New("本轮候选已选完，保留当前结果。")
	ErrCurrentUnavailable = errors.New("当前选项已不可用，请发送 /dinner 换一个。")
	ErrNoCurrent          = errors.New("今天还没有个人推荐，请先发送 /dinner 推荐。")
	ErrAlreadyEaten       = errors.New("今天已吃并记录，明天再推荐。")
)
