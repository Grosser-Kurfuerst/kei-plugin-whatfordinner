package decision

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/whatfordinner/internal/catalog"
	"reflect"
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

func TestPersonalRecommendationUsesConversationProfile(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	cheap, expensive := int64(1800), int64(3000)
	s := service(t, []catalog.Option{
		{ID: "a", Name: "A", Enabled: true, PriceCents: &expensive, Tags: []string{"牛肉"}},
		{ID: "b", Name: "B", Enabled: true, PriceCents: &cheap},
	}, store, &now)
	ctx := context.Background()
	first, err := s.RecommendFor(ctx, scope, Profile{})
	if err != nil || first.Option.ID != "a" {
		t.Fatalf("%+v %v", first, err)
	}
	filtered := Profile{BudgetCents: 1800, ExcludedTags: []string{"牛肉"}}
	second, err := s.RecommendFor(ctx, scope, filtered)
	if err != nil || second.Option.ID != "b" {
		t.Fatalf("changed conversation conditions did not reset personal round: %+v %v", second, err)
	}
	if _, err := s.RerollFor(ctx, scope, filtered); !errors.Is(err, ErrRoundExhausted) {
		t.Fatal(err)
	}
	if _, err := s.MarkEaten(ctx, scope); err != nil {
		t.Fatal(err)
	}
	third, err := s.RecommendFor(ctx, scope, Profile{})
	if err != nil || third.Option.ID != "b" || !third.Eaten {
		t.Fatalf("new conversation conditions replaced eaten snapshot: %+v %v", third, err)
	}
	if records, err := s.History(ctx, scope); err != nil || len(records) != 1 || records[0].Option.ID != "b" {
		t.Fatalf("personal history changed: %+v %v", records, err)
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

func preferenceOptions() []catalog.Option {
	a, b := int64(1800), int64(2000)
	return []catalog.Option{
		{ID: "a", Name: "牛肉面", PriceCents: &a, Tags: []string{"面食", "牛肉"}, Enabled: true},
		{ID: "b", Name: "盖浇饭", PriceCents: &b, Tags: []string{"米饭"}, Enabled: true},
		{ID: "unknown", Name: "未知价格", Enabled: true},
		{ID: "off", Name: "停用", PriceCents: &a},
	}
}

func TestBudgetAndExcludedTagsFilter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		budget  int64
		tags    []string
		unknown bool
		want    string
	}{
		{name: "default", want: "a"},
		{name: "exact budget", budget: 1800, want: "a"},
		{name: "one cent short", budget: 1799},
		{name: "excluded beef", tags: []string{"牛肉"}, want: "b"},
		{name: "exact tag only", tags: []string{"牛"}, want: "a"},
		{name: "combined conditions", budget: 2000, tags: []string{"牛肉"}, want: "b"},
		{name: "no relaxation", budget: 1800, tags: []string{"牛肉"}},
		{name: "any excluded tag", tags: []string{"面食", "米饭"}, want: "unknown"},
		{name: "unknown price unlimited", unknown: true, want: "unknown"},
		{name: "unknown price limited", budget: 10000, unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
			items := preferenceOptions()
			if tc.unknown {
				items = items[2:3]
			}
			store := newTestStore()
			s := service(t, items, store, &now)
			ctx := context.Background()
			if _, err := s.SetBudget(ctx, scope, tc.budget); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetExcludedTags(ctx, scope, tc.tags); err != nil {
				t.Fatal(err)
			}
			writes := store.writes
			got, err := s.Recommend(ctx, scope)
			if tc.want == "" {
				if !errors.Is(err, ErrNoCandidates) || store.writes != writes {
					t.Fatalf("conditions relaxed or state changed: %+v %v", got, err)
				}
			} else if err != nil || got.Option.ID != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestPreferenceChangesRestartRound(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, preferenceOptions()[:2], store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reroll(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBudget(ctx, scope, 1800); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, scope)
	if state.Daily.Current != nil || len(state.Daily.TriedIDs) != 0 || state.Daily.Date != "2026-10-09" {
		t.Fatalf("round not reset: %+v", state)
	}
	got, err := s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "a" {
		t.Fatalf("previously tried candidate not restored: %+v %v", got, err)
	}
	tags := []string{"牛肉", "牛肉"}
	profile, err := s.SetExcludedTags(ctx, scope, tags)
	if err != nil || !reflect.DeepEqual(profile.ExcludedTags, []string{"牛肉"}) {
		t.Fatalf("%+v %v", profile, err)
	}
	tags[0] = "米饭"
	if _, err := s.Recommend(ctx, scope); !errors.Is(err, ErrNoCandidates) {
		t.Fatal(err)
	}
	if _, err := s.SetBudget(ctx, scope, 0); err != nil {
		t.Fatal(err)
	}
	got, err = s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "b" {
		t.Fatalf("budget not cleared: %+v %v", got, err)
	}
	if _, err := s.SetExcludedTags(ctx, scope, []string{"米饭"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "a" {
		t.Fatalf("tags appended instead of replaced: %+v %v", got, err)
	}
	if _, err := s.SetExcludedTags(ctx, scope, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	got, err = s.Reroll(ctx, scope)
	if err != nil || got.Option.ID != "b" {
		t.Fatalf("excluded candidate not restored: %+v %v", got, err)
	}
}

func TestPreferencesPreserveMealAndSurviveMidnight(t *testing.T) {
	now := time.Date(2026, 10, 9, 15, 59, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, preferenceOptions()[:2], store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	meal, err := s.MarkEaten(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := store.Load(ctx, scope)
	if _, err := s.SetBudget(ctx, scope, 1800); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetExcludedTags(ctx, scope, []string{"牛肉"}); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Load(ctx, scope)
	if !reflect.DeepEqual(before.Daily, after.Daily) || !reflect.DeepEqual(before.History, after.History) {
		t.Fatal("preferences erased actual meal")
	}
	s = service(t, preferenceOptions()[:2], store, &now)
	got, err := s.Recommend(ctx, scope)
	if err != nil || !reflect.DeepEqual(got, meal) {
		t.Fatalf("lost eaten snapshot: %+v %v", got, err)
	}
	u2 := scope
	u2.UserID = "other"
	profile, err := s.Profile(ctx, u2)
	if err != nil || profile.BudgetCents != 0 || len(profile.ExcludedTags) != 0 {
		t.Fatalf("preferences leaked: %+v %v", profile, err)
	}
	now = time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC)
	if _, err := s.Recommend(ctx, scope); !errors.Is(err, ErrNoCandidates) {
		t.Fatal(err)
	}
	if _, err := s.SetBudget(ctx, scope, 2000); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Load(ctx, scope)
	if after.Daily.Date != "2026-10-10" || after.Daily.Current != nil || after.Daily.Eaten || len(after.Daily.TriedIDs) != 0 || len(after.History) != 1 {
		t.Fatalf("yesterday's eaten status carried into preference update: %+v", after)
	}
	got, err = s.Recommend(ctx, scope)
	if err != nil || got.Option.ID != "b" || got.Eaten {
		t.Fatalf("new day not using saved preferences: %+v %v", got, err)
	}
}

func TestCurrentNoLongerMatchesPreferences(t *testing.T) {
	for _, change := range []string{"price", "unknown price", "tags"} {
		t.Run(change, func(t *testing.T) {
			now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
			store := newTestStore()
			items := preferenceOptions()[:2]
			s := service(t, items, store, &now)
			ctx := context.Background()
			if _, err := s.SetBudget(ctx, scope, 1800); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetExcludedTags(ctx, scope, []string{"辣"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Recommend(ctx, scope); err != nil {
				t.Fatal(err)
			}
			before, _ := store.Load(ctx, scope)
			switch change {
			case "price":
				price := int64(1801)
				items[0].PriceCents = &price
			case "unknown price":
				items[0].PriceCents = nil
			case "tags":
				items[0].Tags = []string{"辣"}
			}
			s = service(t, items, store, &now)
			if _, err := s.Recommend(ctx, scope); !errors.Is(err, ErrCurrentUnavailable) {
				t.Fatal(err)
			}
			if _, err := s.MarkEaten(ctx, scope); !errors.Is(err, ErrCurrentUnavailable) {
				t.Fatal(err)
			}
			if _, err := s.Reroll(ctx, scope); !errors.Is(err, ErrNoCandidates) {
				t.Fatal(err)
			}
			after, _ := store.Load(ctx, scope)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("invalid current changed state")
			}
		})
	}
}

func TestPreferenceFailuresDoNotChangeState(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, preferenceOptions(), store, &now)
	ctx := context.Background()
	if _, err := s.Recommend(ctx, scope); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Load(ctx, scope)
	writes := store.writes
	failure := errors.New("storage failure")
	setters := []func() (Profile, error){
		func() (Profile, error) { return s.SetBudget(ctx, scope, 1800) },
		func() (Profile, error) { return s.SetExcludedTags(ctx, scope, []string{"牛肉"}) },
	}
	store.loadErr = failure
	if _, err := s.Profile(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	for _, set := range setters {
		if _, err := set(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	store.loadErr = nil
	store.saveErr = failure
	for _, set := range setters {
		if _, err := set(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	store.saveErr = nil
	if _, err := s.SetBudget(ctx, scope, -1); err == nil {
		t.Fatal("negative budget accepted")
	}
	if _, err := s.SetExcludedTags(ctx, scope, []string{"牛肉", " "}); err == nil {
		t.Fatal("blank tag accepted")
	}
	if _, err := s.Profile(ctx, scope); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Load(ctx, scope)
	if !reflect.DeepEqual(before, after) || store.writes != writes {
		t.Fatal("failed or read-only preference command changed state")
	}
}

func TestConcurrentPreferenceUpdatesPreserveBothFields(t *testing.T) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	store := newTestStore()
	s := service(t, preferenceOptions(), store, &now)
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := s.SetBudget(ctx, scope, 1800); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := s.SetExcludedTags(ctx, scope, []string{"牛肉"}); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	profile, err := s.Profile(ctx, scope)
	if err != nil || profile.BudgetCents != 1800 || !reflect.DeepEqual(profile.ExcludedTags, []string{"牛肉"}) {
		t.Fatalf("lost preference update: %+v %v", profile, err)
	}
}
