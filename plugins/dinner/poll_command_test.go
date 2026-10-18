package dinner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RandomLemon/kei/pkg/bot"
)

func pollHandler(t *testing.T) (*testStorage, bot.Handler) {
	t.Helper()
	_, h, storage := setupPluginAt(t, []any{
		map[string]any{"id": "a", "name": "A", "price_cents": 1800, "tags": []string{"川菜"}, "enabled": true},
		map[string]any{"id": "b", "name": "B", "price_cents": 2000, "enabled": true},
	}, testClock)
	return storage, h
}

func invokePoll(t *testing.T, h bot.Handler, user, group string, args ...string) string {
	t.Helper()
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: &bot.User{ID: user}, Command: &bot.Command{Name: "dinner", Args: args}}
	if group != "" {
		e.Channel = &bot.Channel{ID: group, Kind: bot.MessageGroup}
	}
	r := bot.NewNoopReply()
	if err := h(context.Background(), e, r); err != nil {
		t.Fatal(err)
	}
	return r.PlainText()
}

func TestGroupPollCommandsAndPrivateIsolation(t *testing.T) {
	_, h := pollHandler(t)
	first := invokePoll(t, h, "u1", "g1")
	if !strings.Contains(first, "A [a] — 0 票") || !strings.Contains(first, "B [b] — 0 票") {
		t.Fatal(first)
	}
	if got := invokePoll(t, h, "u2", "g1"); got != first {
		t.Fatalf("group members see different lists: %s", got)
	}
	invokePoll(t, h, "u1", "g1", "投票", "a")
	text := invokePoll(t, h, "u2", "g1", "投票", "a")
	if !strings.Contains(text, "[a] — 2 票") || !strings.Contains(text, "2 人投票") {
		t.Fatal(text)
	}
	if got := invokePoll(t, h, "u2", "g1", "投票", "a"); got != text {
		t.Fatalf("duplicate vote changed totals: %s", got)
	}
	text = invokePoll(t, h, "u1", "g1", "投票", "b")
	if !strings.Contains(text, "[a] — 1 票") || !strings.Contains(text, "[b] — 1 票") || !strings.Contains(text, "2 人投票") {
		t.Fatal(text)
	}
	for _, conversation := range [][2]string{{"u1", "g2"}, {"u1", ""}, {"u2", ""}} {
		if got := invokePoll(t, h, conversation[0], conversation[1]); got != first {
			t.Fatalf("poll leaked into %v: %s", conversation, got)
		}
	}
	invokePoll(t, h, "u1", "", "投票", "b")
	if got := invokePoll(t, h, "u2", ""); got != first {
		t.Fatalf("private poll leaked: %s", got)
	}
	if text := invokePoll(t, h, "u1", "g1", "历史"); !strings.Contains(text, "暂无") {
		t.Fatalf("voting recorded a meal: %s", text)
	}
	// The same budget retains votes; a different budget opens a new round.
	invokePoll(t, h, "u1", "g1", "不限")
	if text := invokePoll(t, h, "u2", "g1"); !strings.Contains(text, "2 人投票") {
		t.Fatal(text)
	}
	text = invokePoll(t, h, "u1", "g1", "18")
	if !strings.Contains(text, "[a] — 0 票") || strings.Contains(text, "[b]") {
		t.Fatal(text)
	}
	invokePoll(t, h, "u2", "g1", "投票", "a")
	if text := invokePoll(t, h, "u1", "g1", "18.00"); !strings.Contains(text, "[a] — 1 票") {
		t.Fatal(text)
	}
	invokePoll(t, h, "u1", "g1", "忌口", "川菜")
	if text := invokePoll(t, h, "u2", "g1"); !strings.Contains(text, "暂无可选项") {
		t.Fatal(text)
	}
	invokePoll(t, h, "u1", "g1", "不限")
	if text := invokePoll(t, h, "u2", "g1"); !strings.Contains(text, "[b] — 0 票") || strings.Contains(text, "[a]") {
		t.Fatal(text)
	}
}

func TestPollReplyAndStorageFailures(t *testing.T) {
	storage, h := pollHandler(t)
	invokePoll(t, h, "u1", "g1")
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: &bot.User{ID: "u1"}, Channel: &bot.Channel{ID: "g1", Kind: bot.MessageGroup}, Command: &bot.Command{Name: "dinner", Args: []string{"投票", "a"}}}
	failure := errors.New("send failed")
	r := &failedReply{Reply: bot.NewNoopReply(), err: failure}
	if err := h(context.Background(), e, r); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got := invokePoll(t, h, "u2", "g1"); !strings.Contains(got, "[a] — 1 票") {
		t.Fatalf("saved vote lost after failed send: %s", got)
	}
	storage.setErr = errors.New("save failed")
	e.Command.Args = []string{"投票", "b"}
	noReply := bot.NewNoopReply()
	if err := h(context.Background(), e, noReply); !errors.Is(err, storage.setErr) || noReply.PlainText() != "" {
		t.Fatalf("save failure reported success: %v %q", err, noReply.PlainText())
	}
	storage.setErr = nil
	if got := invokePoll(t, h, "u2", "g1"); !strings.Contains(got, "[a] — 1 票") || !strings.Contains(got, "[b] — 0 票") {
		t.Fatalf("failed vote changed stored totals: %s", got)
	}
	storage.getErr = errors.New("load failed")
	for _, args := range [][]string{{"投票"}, {"投票", "a", "extra"}, {"预算", "25"}} {
		if text := invokePoll(t, h, "u1", "g1", args...); !strings.Contains(text, "晚饭插件用法") {
			t.Fatal(text)
		}
	}
	storage.getErr = nil
	for _, id := range []string{"missing", "-1", "２"} {
		if text := invokePoll(t, h, "u1", "g1", "投票", id); !strings.Contains(text, "id 不在当前列表中") {
			t.Fatal(text)
		}
	}
}

func TestPollScopeUsesMessageKindAndRejectsMissingGroup(t *testing.T) {
	storage, h := pollHandler(t)
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: &bot.User{ID: "u1"}, Message: &bot.Message{Kind: bot.MessageGroup}, Channel: &bot.Channel{ID: "g1"}}
	r := bot.NewNoopReply()
	if err := h(context.Background(), e, r); err != nil {
		t.Fatal(err)
	}
	if got := invokePoll(t, h, "u2", "g1"); got != r.PlainText() {
		t.Fatalf("message kind did not create shared poll: %s", got)
	}
	before := len(storage.values)
	e.Channel = nil
	r = bot.NewNoopReply()
	if err := h(context.Background(), e, r); err != nil || !strings.Contains(r.PlainText(), "无法识别会话") || len(storage.values) != before {
		t.Fatalf("missing group ID mutated state: %v %s", err, r.PlainText())
	}
}
