package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"example.com/whatfordinner/internal/decision"
	"github.com/RandomLemon/kei/pkg/bot"
)

var _ decision.PollStore = (*Store)(nil)

func (s *Store) LoadPoll(ctx context.Context, scope decision.PollScope) (decision.PollState, error) {
	data, err := s.storage.Get(ctx, storageKey("poll", scope))
	if errors.Is(err, bot.ErrNotFound) {
		return decision.PollState{}, nil
	}
	if err != nil {
		return decision.PollState{}, fmt.Errorf("load dinner poll: %w", err)
	}
	var result decision.PollState
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode dinner poll: %w", err)
	}
	return result, nil
}

func (s *Store) SavePoll(ctx context.Context, scope decision.PollScope, value decision.PollState) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode dinner poll: %w", err)
	}
	if err := s.storage.Set(ctx, storageKey("poll", scope), data, 0); err != nil {
		return fmt.Errorf("save dinner poll: %w", err)
	}
	return nil
}
