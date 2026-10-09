package decision

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/whatfordinner/internal/catalog"
	"sync"
	"testing"
	"time"
)

type testStore struct {
	mu               sync.Mutex
	values           map[Scope][]byte
	loadErr, saveErr error
	writes           int
}

func newTestStore() *testStore { return &testStore{values: make(map[Scope][]byte)} }
func (s *testStore) Load(ctx context.Context, scope Scope) (UserState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return UserState{}, err
	}
	if s.loadErr != nil {
		return UserState{}, s.loadErr
	}
	var state UserState
	if data := s.values[scope]; data != nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return state, err
		}
	}
	return state, nil
}
func (s *testStore) Save(ctx context.Context, scope Scope, state UserState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.saveErr != nil {
		return s.saveErr
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	s.values[scope] = data
	s.writes++
	return nil
}

var scope = Scope{Platform: "mock", BotID: "bot", UserID: "u1"}

func options() []catalog.Option {
	return []catalog.Option{{ID: "a", Name: "A", Enabled: true}, {ID: "b", Name: "B", Enabled: true}, {ID: "off", Name: "Off"}}
}
func service(t *testing.T, items []catalog.Option, store *testStore, now *time.Time) *Service {
	t.Helper()
	c, err := catalog.New(items)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(c, store, func() time.Time { return *now }, func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDailyDecisionAndReroll(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, options(), store, &now)
	first, err := s.Recommend(ctx, scope)
	if err != nil || first.Option.ID != "a" {
		t.Fatalf("%+v %v", first, err)
	}
	again, err := s.Recommend(ctx, scope)
	if err != nil || again.Option.ID != "a" || store.writes != 1 {
		t.Fatalf("%+v %v writes=%d", again, err, store.writes)
	}
	other, err := s.Reroll(ctx, scope)
	if err != nil || other.Option.ID != "b" {
		t.Fatalf("%+v %v", other, err)
	}
	if _, err := s.Reroll(ctx, scope); !errors.Is(err, ErrRoundExhausted) {
		t.Fatal(err)
	}
	current, _ := s.Recommend(ctx, scope)
	if current.Option.ID != "b" || store.writes != 2 {
		t.Fatalf("%+v writes=%d", current, store.writes)
	}
	restarted := service(t, options(), store, &now)
	current, err = restarted.Recommend(ctx, scope)
	if err != nil || current.Option.ID != "b" {
		t.Fatalf("restart: %+v %v", current, err)
	}
}

func TestShanghaiDateBoundary(t *testing.T) {
	now := time.Date(2026, 10, 9, 15, 59, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reroll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC)
	got, err := s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "a" {
		t.Fatalf("%+v %v", got, err)
	}
	saved, _ := store.Load(ctx, scope)
	if saved.Daily.Date != "2026-10-10" || len(saved.Daily.TriedIDs) != 1 {
		t.Fatalf("%+v", saved)
	}
}

func TestCatalogChangeAndNoCandidates(t *testing.T) {
	now := time.Now()
	store := newTestStore()
	ctx := context.Background()
	s := service(t, options(), store, &now)
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	changed := options()
	price := int64(2200)
	changed[0].Name = "New A"
	changed[0].PriceCents = &price
	s = service(t, changed, store, &now)
	got, err := s.Recommend(ctx, scope)
	if err != nil || got.Option.Name != "New A" || *got.Option.PriceCents != 2200 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, items := range [][]catalog.Option{{changed[1]}, {{ID: "a", Name: "A"}, changed[1]}} {
		s = service(t, items, store, &now)
		if _, err := s.Recommend(ctx, scope); !errors.Is(err, ErrCurrentUnavailable) {
			t.Fatal(err)
		}
	}
	got, err = s.Reroll(ctx, scope)
	if err != nil || got.Option.ID != "b" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, items := range [][]catalog.Option{nil, {{ID: "x", Name: "X"}}} {
		s = service(t, items, newTestStore(), &now)
		if _, err := s.Recommend(ctx, scope); !errors.Is(err, ErrNoCandidates) {
			t.Fatal(err)
		}
	}
}

func TestIsolationAndConcurrentRecommend(t *testing.T) {
	now := time.Now()
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.Recommend(ctx, scope)
			if err != nil || got.Option.ID != "a" {
				t.Errorf("%+v %v", got, err)
			}
		}()
	}
	wg.Wait()
	if store.writes != 1 {
		t.Fatalf("writes=%d", store.writes)
	}
	u2 := scope
	u2.UserID = "u2"
	if _, err := s.Recommend(ctx, u2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reroll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Recommend(ctx, u2)
	if got.Option.ID != "a" {
		t.Fatal(got)
	}
	otherBot := scope
	otherBot.BotID = "other"
	got, err := s.Recommend(ctx, otherBot)
	if err != nil || got.Option.ID != "a" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestStoreFailureAndCancelledContext(t *testing.T) {
	now := time.Now()
	store := newTestStore()
	s := service(t, options(), store, &now)
	failure := errors.New("disk failure")
	ctx := context.Background()
	store.loadErr = failure
	if _, err := s.Recommend(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	store.loadErr = nil
	store.saveErr = failure
	if _, err := s.Recommend(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	store.saveErr = nil
	saved, _ := store.Load(ctx, scope)
	if saved.Daily.Current != nil {
		t.Fatalf("failed save modified state: %+v", saved)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Recommend(cancelled, scope); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "a" {
		t.Fatalf("%+v %v", got, err)
	}
}
