package state

import (
	"context"
	"errors"
	"example.com/whatfordinner/internal/decision"
	"github.com/RandomLemon/kei/pkg/bot"
	"testing"
	"time"
)

type memoryStorage struct {
	values         map[string][]byte
	getErr, setErr error
	ttl            time.Duration
}

func (s *memoryStorage) Get(_ context.Context, key string) ([]byte, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if v, ok := s.values[key]; ok {
		return append([]byte(nil), v...), nil
	}
	return nil, bot.ErrNotFound
}
func (s *memoryStorage) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = append([]byte(nil), value...)
	s.ttl = ttl
	return nil
}
func (s *memoryStorage) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

func TestStateRoundTripAndIsolation(t *testing.T) {
	memory := &memoryStorage{values: map[string][]byte{}}
	store := New(memory)
	ctx := context.Background()
	scope := decision.Scope{Platform: "mock", BotID: "b", UserID: "u"}
	got, err := store.Load(ctx, scope)
	if err != nil || got.Daily.Current != nil {
		t.Fatalf("%+v %v", got, err)
	}
	price := int64(1800)
	value := decision.UserState{Daily: decision.DailyDecision{Date: "2026-10-09", Current: &decision.OptionSnapshot{ID: "a", Name: "A", PriceCents: &price}, TriedIDs: []string{"a"}}}
	if err := store.Save(ctx, scope, value); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load(ctx, scope)
	if err != nil || got.Daily.Current.ID != "a" || *got.Daily.Current.PriceCents != 1800 || memory.ttl != 0 {
		t.Fatalf("%+v %v ttl=%v", got, err, memory.ttl)
	}
	got.Daily.TriedIDs[0] = "changed"
	*got.Daily.Current.PriceCents = 1
	got, _ = store.Load(ctx, scope)
	if got.Daily.TriedIDs[0] != "a" || *got.Daily.Current.PriceCents != 1800 {
		t.Fatal(got)
	}
	for _, other := range []decision.Scope{{Platform: "different", BotID: "b", UserID: "u"}, {Platform: "mock", BotID: "different", UserID: "u"}, {Platform: "mock", BotID: "b", UserID: "different"}} {
		got, err = store.Load(ctx, other)
		if err != nil || got.Daily.Current != nil {
			t.Fatalf("scope leak: %+v %v", got, err)
		}
	}
	left := decision.Scope{Platform: "a:b", BotID: "c", UserID: "d"}
	right := decision.Scope{Platform: "a", BotID: "b:c", UserID: "d"}
	if key(left) == key(right) {
		t.Fatal("ambiguous scope encoding")
	}
}

func TestStorageErrors(t *testing.T) {
	memory := &memoryStorage{values: map[string][]byte{}}
	store := New(memory)
	ctx := context.Background()
	scope := decision.Scope{UserID: "u"}
	failure := errors.New("failure")
	memory.getErr = failure
	if _, err := store.Load(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	memory.getErr = nil
	memory.values[key(scope)] = []byte("broken JSON")
	if _, err := store.Load(ctx, scope); err == nil {
		t.Fatal("corruption ignored")
	}
	memory.setErr = failure
	if err := store.Save(ctx, scope, decision.UserState{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
