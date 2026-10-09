// Package state 将个人决策状态保存到 kei 提供的键值存储。
package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"example.com/whatfordinner/internal/decision"
	"github.com/RandomLemon/kei/pkg/bot"
)

// Store 通过单个 JSON 值保存一份个人状态。
type Store struct{ storage bot.Storage }

var _ decision.Store = (*Store)(nil)

// New 使用宿主提供的存储，不自行打开数据库。
func New(storage bot.Storage) *Store { return &Store{storage: storage} }

func key(scope decision.Scope) string {
	data, _ := json.Marshal(scope) // Scope 仅包含固定顺序的字符串字段，编码不会失败。
	return fmt.Sprintf("dinner:state:%x", sha256.Sum256(data))
}

// Load 仅在键不存在时返回默认状态，其他错误保留错误链。
func (s *Store) Load(ctx context.Context, scope decision.Scope) (decision.UserState, error) {
	data, err := s.storage.Get(ctx, key(scope))
	if errors.Is(err, bot.ErrNotFound) {
		return decision.UserState{}, nil
	}
	if err != nil {
		return decision.UserState{}, fmt.Errorf("load dinner state: %w", err)
	}
	var result decision.UserState
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode dinner state: %w", err)
	}
	return result, nil
}

// Save 一次覆盖完整状态，TTL=0 表示不因存储过期而丢失。
func (s *Store) Save(ctx context.Context, scope decision.Scope, value decision.UserState) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode dinner state: %w", err)
	}
	if err := s.storage.Set(ctx, key(scope), data, 0); err != nil {
		return fmt.Errorf("save dinner state: %w", err)
	}
	return nil
}
