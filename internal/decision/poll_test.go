package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"example.com/whatfordinner/internal/catalog"
)

type testPollStore struct {
	mu               sync.Mutex
	values           map[PollScope][]byte
	loadErr, saveErr error
	writes           int
}

func newTestPollStore() *testPollStore { return &testPollStore{values: make(map[PollScope][]byte)} }

func (s *testPollStore) LoadPoll(ctx context.Context, scope PollScope) (PollState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PollState{}, err
	}
	if s.loadErr != nil {
		return PollState{}, s.loadErr
	}
	var state PollState
	if data := s.values[scope]; data != nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return state, err
		}
	}
	return state, nil
}

func (s *testPollStore) SavePoll(ctx context.Context, scope PollScope, state PollState) error {
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

var group = PollScope{Platform: "mock", BotID: "bot", Kind: "group", ID: "g1"}

func TestPollListVoteAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	personalStore, pollStore := newTestStore(), newTestPollStore()
	personal := service(t, preferenceOptions(), personalStore, &now)
	polls := NewPollService(personal, pollStore)
	budget := int64(2000)
	first, err := polls.List(ctx, group, scope, &budget)
	if err != nil || len(first.Entries) != 2 || first.Entries[0].Option.ID != "a" || first.Entries[1].Option.ID != "b" || first.Voters != 0 {
		t.Fatalf("%+v %v", first, err)
	}
	other := scope
	other.UserID = "u2"
	second, err := polls.List(ctx, group, other, nil)
	if err != nil || !reflect.DeepEqual(first, second) || pollStore.writes != 1 || personalStore.writes != 0 {
		t.Fatalf("shared list changed or created a meal: %+v %v", second, err)
	}
	view, err := polls.Vote(ctx, group, "u1", 1)
	if err != nil || view.Entries[0].Votes != 1 || view.Voters != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	writes := pollStore.writes
	if _, err := polls.Vote(ctx, group, "u1", 1); err != nil || pollStore.writes != writes {
		t.Fatalf("duplicate vote wrote again: %v", err)
	}
	if _, err := polls.Vote(ctx, group, "u2", 2); err != nil {
		t.Fatal(err)
	}
	view, err = polls.Vote(ctx, group, "u1", 2)
	if err != nil || view.Entries[0].Votes != 0 || view.Entries[1].Votes != 2 || view.Voters != 2 {
		t.Fatalf("revote did not transfer the old vote: %+v %v", view, err)
	}
	if view.Entries[0].Number != 1 || view.Entries[0].Option.ID != "a" || view.Entries[1].Number != 2 {
		t.Fatal("vote counts reordered identifiers")
	}
	polls = NewPollService(service(t, preferenceOptions(), personalStore, &now), pollStore)
	restored, err := polls.List(ctx, group, other, &budget)
	if err != nil || !reflect.DeepEqual(view, restored) || personalStore.writes != 0 {
		t.Fatalf("restart or same budget erased votes: %+v %v", restored, err)
	}
	for _, separate := range []PollScope{
		{Platform: "mock", BotID: "bot", Kind: "group", ID: "g2"},
		{Platform: "mock", BotID: "bot", Kind: "private", ID: "g1"},
		{Platform: "mock", BotID: "other", Kind: "group", ID: "g1"},
		{Platform: "other", BotID: "bot", Kind: "group", ID: "g1"},
	} {
		if _, err := polls.Vote(ctx, separate, "u1", 1); !errors.Is(err, ErrNoPoll) {
			t.Fatalf("scope leaked: %+v %v", separate, err)
		}
	}
}

func TestPollFiltersAndResets(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 15, 59, 0, 0, time.UTC)
	store := newTestPollStore()
	polls := NewPollService(service(t, preferenceOptions(), newTestStore(), &now), store)
	budget := int64(1800)
	view, err := polls.List(ctx, group, scope, &budget)
	if err != nil || len(view.Entries) != 1 || view.Entries[0].Option.ID != "a" {
		t.Fatalf("%+v %v", view, err)
	}
	if _, err := polls.Vote(ctx, group, "u1", 1); err != nil {
		t.Fatal(err)
	}
	budget = 1799
	if _, err := polls.List(ctx, group, scope, &budget); !errors.Is(err, ErrNoCandidates) {
		t.Fatal(err)
	}
	saved, _ := store.LoadPoll(ctx, group)
	if saved.Profile.BudgetCents != 1799 || len(saved.Options) != 0 || len(saved.Ballots) != 0 {
		t.Fatal("unmatched budget was relaxed or votes survived reset")
	}
	budget = 0
	view, err = polls.List(ctx, group, scope, &budget)
	if err != nil || len(view.Entries) != 3 || view.Entries[2].Option.ID != "unknown" {
		t.Fatalf("unknown price excluded when unlimited: %+v %v", view, err)
	}
	if _, err := polls.SetExcludedTags(ctx, group, []string{"牛肉", "牛肉"}); err != nil {
		t.Fatal(err)
	}
	if _, err := polls.Vote(ctx, group, "u1", 1); !errors.Is(err, ErrNoPoll) {
		t.Fatal(err)
	}
	view, err = polls.List(ctx, group, scope, nil)
	if err != nil || view.Entries[0].Option.ID != "b" || len(view.Entries) != 2 || len(view.Profile.ExcludedTags) != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	if _, err := polls.Vote(ctx, group, "u1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := polls.SetExcludedTags(ctx, group, []string{"牛肉"}); err != nil {
		t.Fatal(err)
	}
	view, err = polls.List(ctx, group, scope, nil)
	if err != nil || view.Entries[0].Votes != 1 {
		t.Fatal("unchanged exclusions erased votes")
	}
	now = time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	if _, err := polls.Vote(ctx, group, "u1", 1); !errors.Is(err, ErrNoPoll) {
		t.Fatal("yesterday's numbered list accepted a new vote")
	}
	view, err = polls.List(ctx, group, scope, nil)
	if err != nil || view.Date != "2026-10-11" || view.Voters != 0 || len(view.Profile.ExcludedTags) != 1 {
		t.Fatalf("new day failed to preserve preferences/reset votes: %+v %v", view, err)
	}
	if _, err := polls.SetExcludedTags(ctx, group, nil); err != nil {
		t.Fatal(err)
	}
	view, err = polls.List(ctx, group, scope, nil)
	if err != nil || view.Entries[0].Option.ID != "a" {
		t.Fatalf("clear exclusions did not restore candidate: %+v %v", view, err)
	}
}

func TestPollFiveWeightedDistinctCandidates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	personalStore := newTestStore()
	var items []catalog.Option
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("%c", 'a'+i)
		items = append(items, catalog.Option{ID: id, Name: id, Enabled: true})
	}
	if err := personalStore.Save(ctx, scope, UserState{History: []MealRecord{{Date: "2026-10-09", Option: OptionSnapshot{ID: "a"}}}}); err != nil {
		t.Fatal(err)
	}
	personal := service(t, items, personalStore, &now)
	personal.randomFloat = func() float64 { return 0.2 }
	polls := NewPollService(personal, newTestPollStore())
	view, err := polls.List(ctx, group, scope, nil)
	if err != nil || len(view.Entries) != 5 {
		t.Fatalf("%+v %v", view, err)
	}
	for i, entry := range view.Entries {
		if entry.Option.ID != fmt.Sprintf("%c", 'b'+i) {
			t.Fatalf("history weight or sampling without replacement not applied: %+v", view)
		}
	}
	if personalStore.writes != 1 {
		t.Fatal("list generation wrote a personal recommendation or meal")
	}
}

func TestConcurrentPollCreationAndVotes(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	store := newTestPollStore()
	polls := NewPollService(service(t, options(), newTestStore(), &now), store)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, err := polls.List(ctx, group, scope, nil)
			if err != nil || view.Entries[0].Option.ID != "a" {
				t.Errorf("%+v %v", view, err)
			}
		}()
	}
	wg.Wait()
	if store.writes != 1 {
		t.Fatalf("concurrent creation wrote %d rounds", store.writes)
	}
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			voter := fmt.Sprintf("user-%d", i)
			if _, err := polls.Vote(ctx, group, voter, 1); err != nil {
				t.Error(err)
			}
			if _, err := polls.Vote(ctx, group, voter, 1); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	view, err := polls.List(ctx, group, scope, nil)
	if err != nil || view.Voters != 30 || view.Entries[0].Votes != 30 || store.writes != 31 {
		t.Fatalf("lost or duplicate votes: %+v %v writes=%d", view, err, store.writes)
	}
}

func TestPollFailureAndCatalogChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	store := newTestPollStore()
	personalStore := newTestStore()
	items := preferenceOptions()[:2]
	polls := NewPollService(service(t, items, personalStore, &now), store)
	budget := int64(2000)
	if _, err := polls.List(ctx, group, scope, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := polls.Vote(ctx, group, "u1", 1); err != nil {
		t.Fatal(err)
	}
	before, _ := store.LoadPoll(ctx, group)
	writes := store.writes
	for _, number := range []int{0, -1, 3} {
		if _, err := polls.Vote(ctx, group, "u2", number); !errors.Is(err, ErrInvalidVote) {
			t.Fatal(err)
		}
	}
	failure := errors.New("disk failure")
	operations := []func() error{
		func() error { b := int64(2500); _, err := polls.List(ctx, group, scope, &b); return err },
		func() error { _, err := polls.Vote(ctx, group, "u2", 2); return err },
		func() error { _, err := polls.SetExcludedTags(ctx, group, []string{"牛肉"}); return err },
	}
	store.loadErr = failure
	for _, operation := range operations {
		if err := operation(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if _, err := polls.Profile(ctx, group); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	store.loadErr = nil
	store.saveErr = failure
	for _, operation := range operations {
		if err := operation(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	store.saveErr = nil
	after, _ := store.LoadPoll(ctx, group)
	if !reflect.DeepEqual(before, after) || writes != store.writes {
		t.Fatal("invalid input or failed storage changed the poll")
	}
	price := int64(2001)
	items[0].PriceCents = &price
	polls = NewPollService(service(t, items, personalStore, &now), store)
	if _, err := polls.Vote(ctx, group, "u2", 1); !errors.Is(err, ErrPollOptionUnavailable) {
		t.Fatal(err)
	}
	view, err := polls.List(ctx, group, scope, nil)
	if err != nil || view.Entries[0].Available || view.Entries[0].Votes != 1 || view.Entries[1].Number != 2 {
		t.Fatalf("changed catalog silently reindexed the poll: %+v %v", view, err)
	}
	polls = NewPollService(service(t, nil, personalStore, &now), store)
	if _, err := polls.Vote(ctx, group, "u2", 1); !errors.Is(err, ErrPollOptionUnavailable) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := polls.Vote(cancelled, group, "u2", 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
