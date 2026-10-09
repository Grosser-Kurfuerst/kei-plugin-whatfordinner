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

func TestWeightedServiceUsesOnlyMeals(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reroll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, scope)
	if len(state.History) != 0 {
		t.Fatal("recommendations became meals")
	}
	state.Daily = DailyDecision{}
	state.History = []MealRecord{{Date: "2026-10-08", Option: OptionSnapshot{ID: "a", Name: "A"}}}
	if err := store.Save(ctx, scope, state); err != nil {
		t.Fatal(err)
	}
	s.randomFloat = func() float64 { return 0.2 }
	got, err := s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "b" || got.RecentMeals != 0 {
		t.Fatalf("history not used in selection: %+v %v", got, err)
	}
	got, err = s.Reroll(ctx, scope)
	if err != nil || got.Option.ID != "a" || got.RecentMeals != 1 {
		t.Fatalf("remaining weight not renormalized: %+v %v", got, err)
	}
	state, _ = store.Load(ctx, scope)
	if len(state.History) != 1 {
		t.Fatal("reroll became a meal")
	}
}

func TestMealRecordingAndSnapshot(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	items := options()
	price := int64(1800)
	items[0].PriceCents = &price
	items[0].Venue = "Old venue"
	s := service(t, items, store, &now)
	ctx := context.Background()
	if _, err := s.MarkEaten(ctx, scope); !errors.Is(err, ErrNoCurrent) {
		t.Fatal(err)
	}
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	got, err := s.MarkEaten(ctx, scope)
	if err != nil || !got.Eaten {
		t.Fatalf("%+v %v", got, err)
	}
	writes := store.writes
	if _, err := s.MarkEaten(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if store.writes != writes {
		t.Fatal("duplicate meal save")
	}
	history, err := s.History(ctx, scope)
	if err != nil || len(history) != 1 || history[0].Option.ID != "a" || !history[0].EatenAt.Equal(now) || history[0].EatenAt.Location() != time.UTC {
		t.Fatalf("%+v %v", history, err)
	}
	if _, err := s.Reroll(ctx, scope); !errors.Is(err, ErrAlreadyEaten) {
		t.Fatal(err)
	}
	// Removing all current catalog entries cannot erase an actual meal.
	s = service(t, nil, store, &now)
	got, err = s.Recommend(ctx, scope)
	if err != nil || !got.Eaten || got.Option.Name != "A" || got.Option.Venue != "Old venue" || *got.Option.PriceCents != 1800 {
		t.Fatalf("lost snapshot: %+v %v", got, err)
	}
	now = now.AddDate(0, 0, 1)
	if _, err := s.MarkEaten(ctx, scope); !errors.Is(err, ErrNoCurrent) {
		t.Fatal(err)
	}
	s = service(t, options(), store, &now)
	got, err = s.Recommend(ctx, scope)
	if err != nil || got.Eaten {
		t.Fatalf("day not reset: %+v %v", got, err)
	}
	history, err = s.History(ctx, scope)
	if err != nil || len(history) != 1 {
		t.Fatalf("history lost: %+v %v", history, err)
	}
}

func TestConcurrentAndFailedMealSave(t *testing.T) {
	now := time.Now()
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("failed save")
	store.saveErr = failure
	if _, err := s.MarkEaten(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, scope)
	if state.Daily.Eaten || len(state.History) != 0 {
		t.Fatal("failed save recorded a meal")
	}
	store.saveErr = nil
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.MarkEaten(ctx, scope)
			if err != nil || !got.Eaten {
				t.Errorf("%+v %v", got, err)
			}
		}()
	}
	wg.Wait()
	state, _ = store.Load(ctx, scope)
	if len(state.History) != 1 || store.writes != 2 {
		t.Fatalf("history=%d writes=%d", len(state.History), store.writes)
	}
	store.loadErr = failure
	if _, err := s.History(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}

func TestHistoryWindowAndLimit(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	local := now.In(s.location)
	var records []MealRecord
	for d := 31; d >= 1; d-- {
		records = append(records, MealRecord{Date: local.AddDate(0, 0, -d).Format(time.DateOnly), Option: OptionSnapshot{ID: "a", Name: "Old A"}})
	}
	if err := store.Save(ctx, scope, UserState{History: records}); err != nil {
		t.Fatal(err)
	}
	history, err := s.History(ctx, scope)
	if err != nil || len(history) != 7 || history[0].Date != "2026-10-08" || history[6].Date != "2026-10-02" {
		t.Fatalf("%+v %v", history, err)
	}
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, scope)
	if len(state.History) != 29 {
		t.Fatalf("old history retained: %d", len(state.History))
	}
	for _, r := range state.History {
		if r.Date < local.AddDate(0, 0, -29).Format(time.DateOnly) {
			t.Fatal(r)
		}
	}
	if _, err := s.MarkEaten(ctx, scope); err != nil {
		t.Fatal(err)
	}
	state, _ = store.Load(ctx, scope)
	if len(state.History) != 30 {
		t.Fatal(len(state.History))
	}
	history, err = s.History(ctx, scope)
	if err != nil || len(history) != 7 || history[0].Date != "2026-10-09" {
		t.Fatalf("%+v %v", history, err)
	}
}

func TestMarkCurrentValidationAndLatestSnapshot(t *testing.T) {
	now := time.Now()
	store := newTestStore()
	s := service(t, options(), store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	missing := service(t, options()[1:], store, &now)
	if _, err := missing.MarkEaten(ctx, scope); !errors.Is(err, ErrCurrentUnavailable) {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, scope)
	if state.Daily.Eaten || len(state.History) != 0 {
		t.Fatal("unavailable current was recorded")
	}
	changed := options()
	price := int64(2300)
	changed[0].Name = "Latest A"
	changed[0].Venue = "Latest venue"
	changed[0].PriceCents = &price
	updated := service(t, changed, store, &now)
	got, err := updated.MarkEaten(ctx, scope)
	if err != nil || got.Option.Name != "Latest A" || *got.Option.PriceCents != 2300 {
		t.Fatalf("%+v %v", got, err)
	}
	history, err := updated.History(ctx, scope)
	if err != nil || history[0].Option.Venue != "Latest venue" || *history[0].Option.PriceCents != 2300 {
		t.Fatalf("%+v %v", history, err)
	}
}
