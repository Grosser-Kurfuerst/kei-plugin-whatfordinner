package state

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"example.com/whatfordinner/internal/decision"
)

func TestPollRoundTripAndScopeIsolation(t *testing.T) {
	memory := &memoryStorage{values: map[string][]byte{}}
	store := New(memory)
	ctx := context.Background()
	scope := decision.PollScope{Platform: "mock", BotID: "bot", Kind: "group", ID: "g1"}
	empty, err := store.LoadPoll(ctx, scope)
	if err != nil || len(empty.Options) != 0 || len(empty.Ballots) != 0 {
		t.Fatalf("%+v %v", empty, err)
	}
	value := decision.PollState{Date: "2026-10-10", Profile: decision.Profile{BudgetCents: 2500, ExcludedTags: []string{"辣"}}, Options: []decision.OptionSnapshot{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, Ballots: map[string]string{"u1": "b", "u2": "b"}}
	if err := store.SavePoll(ctx, scope, value); err != nil {
		t.Fatal(err)
	}
	restored, err := New(memory).LoadPoll(ctx, scope)
	if err != nil || !reflect.DeepEqual(value, restored) || memory.ttl != 0 {
		t.Fatalf("%+v %v", restored, err)
	}
	restored.Options[0].Name = "changed"
	restored.Ballots["u1"] = "a"
	for _, other := range []decision.PollScope{
		{Platform: "mock", BotID: "bot", Kind: "private", ID: "g1"},
		{Platform: "mock", BotID: "bot", Kind: "group", ID: "g2"},
		{Platform: "mock", BotID: "other", Kind: "group", ID: "g1"},
		{Platform: "other", BotID: "bot", Kind: "group", ID: "g1"},
	} {
		got, err := store.LoadPoll(ctx, other)
		if err != nil || len(got.Options) != 0 {
			t.Fatalf("scope leak: %+v %v", got, err)
		}
	}
	personal, err := store.Load(ctx, decision.Scope{Platform: "mock", BotID: "bot", UserID: "g1"})
	if err != nil || personal.Daily.Current != nil {
		t.Fatalf("poll overwrote personal state: %+v %v", personal, err)
	}
	got, _ := store.LoadPoll(ctx, scope)
	if !reflect.DeepEqual(value, got) {
		t.Fatal("caller mutated persisted poll")
	}
}

func TestPollStorageErrors(t *testing.T) {
	memory := &memoryStorage{values: map[string][]byte{}}
	store := New(memory)
	ctx := context.Background()
	scope := decision.PollScope{ID: "g1", Kind: "group"}
	failure := errors.New("storage failure")
	memory.getErr = failure
	if _, err := store.LoadPoll(ctx, scope); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	memory.getErr = nil
	memory.values[pollKey(scope)] = []byte("broken JSON")
	if _, err := store.LoadPoll(ctx, scope); err == nil {
		t.Fatal("corrupted poll accepted")
	}
	memory.setErr = failure
	if err := store.SavePoll(ctx, scope, decision.PollState{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
