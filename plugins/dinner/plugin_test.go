package dinner

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/whatfordinner/internal/decision"
	"example.com/whatfordinner/internal/state"
	"github.com/RandomLemon/kei/pkg/bot"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupPlugin(t *testing.T, items []any) (*Plugin, bot.Handler) {
	t.Helper()
	p := &Plugin{}
	reg := bot.NewRecordingRegistrar()
	ctx := bot.WithPluginContext(context.Background(), bot.PluginContext{Storage: &testStorage{values: make(map[string][]byte)}, Config: bot.NewConfig(map[string]any{"catalog": map[string]any{"items": items}})})
	if err := p.Setup(ctx, reg); err != nil {
		t.Fatal(err)
	}
	rule, ok := reg.Command("dinner")
	if !ok {
		t.Fatal("missing command")
	}
	return p, rule.Handler
}

func invoke(t *testing.T, h bot.Handler, sender *bot.User, args ...string) string {
	t.Helper()
	r := bot.NewNoopReply()
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: sender, Command: &bot.Command{Name: "dinner", Args: args}}
	if err := h(context.Background(), e, r); err != nil {
		t.Fatal(err)
	}
	return r.PlainText()
}

func TestCommands(t *testing.T) {
	_, h := setupPlugin(t, []any{map[string]any{"id": "a", "name": "牛肉面", "enabled": false, "price_cents": 1800}, map[string]any{"id": "b", "name": "B", "enabled": true}})
	u := &bot.User{ID: "u1"}
	for _, args := range [][]string{{"帮助"}, {"unknown"}, {"候选", "extra"}} {
		if text := invoke(t, h, u, args...); !strings.Contains(text, "/dinner 帮助") {
			t.Fatalf("help = %q", text)
		}
	}
	text := invoke(t, h, u, "候选")
	for _, want := range []string{"牛肉面", "18.00", "停用", "价格未知"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if text := invoke(t, h, nil, "候选"); !strings.Contains(text, "无法识别") {
		t.Fatal(text)
	}
	if text := invoke(t, h, &bot.User{ID: " "}); !strings.Contains(text, "无法识别") {
		t.Fatal(text)
	}
}

func TestEmptyCatalogAndInvalidSetup(t *testing.T) {
	_, h := setupPlugin(t, nil)
	if text := invoke(t, h, &bot.User{ID: "u"}, "候选"); !strings.Contains(text, "清单为空") {
		t.Fatal(text)
	}
	p := &Plugin{}
	reg := bot.NewRecordingRegistrar()
	if err := p.Setup(context.Background(), reg); err == nil {
		t.Fatal("expected missing context")
	}
	ctx := bot.WithPluginContext(context.Background(), bot.PluginContext{Config: bot.NewConfig(map[string]any{"catalog": map[string]any{"items": []any{map[string]any{"id": "a"}}}})})
	if err := p.Setup(ctx, reg); err == nil {
		t.Fatal("expected invalid catalog")
	}
}

type testStorage struct {
	mu             sync.Mutex
	values         map[string][]byte
	getErr, setErr error
}

func (s *testStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	v, ok := s.values[key]
	if !ok {
		return nil, bot.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s *testStorage) Set(_ context.Context, key string, v []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.values[key] = append([]byte(nil), v...)
	return nil
}
func (s *testStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

type failedReply struct {
	bot.Reply
	err error
}

func (r *failedReply) Text(text string) bot.Reply { r.Reply.Text(text); return r }
func (r *failedReply) Send(context.Context) error { return r.err }

func TestRecommendationsAndReplyFailure(t *testing.T) {
	_, h := setupPlugin(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}, map[string]any{"id": "b", "name": "B", "enabled": true}})
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: &bot.User{ID: "u"}, Command: &bot.Command{Name: "dinner"}}
	failure := errors.New("send failure")
	underlying := bot.NewNoopReply()
	r := &failedReply{Reply: underlying, err: failure}
	if err := h(context.Background(), e, r); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	for _, group := range []string{"g1", "g2", ""} {
		e.Channel = &bot.Channel{ID: group}
		reply := bot.NewNoopReply()
		if err := h(context.Background(), e, reply); err != nil {
			t.Fatal(err)
		}
		if reply.PlainText() != underlying.PlainText() {
			t.Fatalf("different personal result: %s", reply.PlainText())
		}
	}
	first := invoke(t, h, e.Sender)
	other := invoke(t, h, e.Sender, "换一个")
	if first == other {
		t.Fatal("reroll repeated")
	}
	if text := invoke(t, h, e.Sender, "换一个"); !strings.Contains(text, "选完") {
		t.Fatal(text)
	}
}

func TestHandlerStorageFailureDoesNotReplySuccess(t *testing.T) {
	p := &Plugin{}
	reg := bot.NewRecordingRegistrar()
	failure := errors.New("storage failure")
	storage := &testStorage{values: map[string][]byte{}, setErr: failure}
	ctx := bot.WithPluginContext(context.Background(), bot.PluginContext{Storage: storage, Config: bot.NewConfig(map[string]any{"catalog": map[string]any{"items": []any{map[string]any{"id": "a", "name": "A", "enabled": true}}}})})
	if err := p.Setup(ctx, reg); err != nil {
		t.Fatal(err)
	}
	rule, _ := reg.Command("dinner")
	r := bot.NewNoopReply()
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: &bot.User{ID: "u"}, Command: &bot.Command{Name: "dinner"}}
	if err := rule.Handler(context.Background(), e, r); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if r.PlainText() != "" {
		t.Fatalf("false success: %s", r.PlainText())
	}
}

func TestMealCommands(t *testing.T) {
	_, h := setupPlugin(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}})
	u := &bot.User{ID: "meal"}
	if text := invoke(t, h, u, "已吃"); !strings.Contains(text, "还没有推荐") {
		t.Fatal(text)
	}
	invoke(t, h, u)
	if text := invoke(t, h, u, "历史"); !strings.Contains(text, "暂无") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u, "已吃"); !strings.Contains(text, "已吃并记录") {
		t.Fatal(text)
	}
	invoke(t, h, u, "已吃")
	if text := invoke(t, h, u, "历史"); strings.Count(text, "[a]") != 1 {
		t.Fatal(text)
	}
	if text := invoke(t, h, u, "换一个"); !strings.Contains(text, "已吃") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u); !strings.Contains(text, "已吃") {
		t.Fatal(text)
	}
}

func TestParseBudget(t *testing.T) {
	for text, want := range map[string]int64{"不限": 0, "25": 2500, "25.0": 2500, "25.00": 2500, "0.01": 1, "0018.05": 1805, "92233720368547758.07": 9223372036854775807} {
		if got, valid := parseBudget(text); !valid || got != want {
			t.Errorf("%q: %d, %v; want %d", text, got, valid, want)
		}
	}
	for _, text := range []string{"", "0", "0.00", "-1", "+1", "1.001", "1.", ".50", "1.2.3", "NaN", "1e2", "25元", " 25", "２５", "92233720368547758.08"} {
		if _, valid := parseBudget(text); valid {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestPreferenceCommandsAndInvalidInput(t *testing.T) {
	p, h := setupPlugin(t, []any{
		map[string]any{"id": "a", "name": "牛肉面", "price_cents": 1800, "tags": []string{"牛肉"}, "enabled": true},
		map[string]any{"id": "b", "name": "盖浇饭", "price_cents": 2000, "tags": []string{"米饭"}, "enabled": true},
	})
	u := &bot.User{ID: "pref"}
	if text := invoke(t, h, u, "帮助"); !strings.Contains(text, "当前预算：不限") || !strings.Contains(text, "当前忌口：无") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u, "预算", "18.00"); !strings.Contains(text, "当前预算：18.00 元") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u); !strings.Contains(text, "[a]") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u, "忌口", "牛肉", "牛肉"); !strings.Contains(text, "当前忌口：牛肉") || strings.Contains(text, "牛肉、牛肉") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u); !strings.Contains(text, "暂无") {
		t.Fatal(text)
	}
	invoke(t, h, u, "忌口", "清空")
	invoke(t, h, u)
	personal := decision.Scope{Platform: "mock", BotID: "bot", UserID: u.ID}
	before, err := p.service.Profile(context.Background(), personal)
	if err != nil {
		t.Fatal(err)
	}
	first := invoke(t, h, u)
	for _, args := range [][]string{
		{"预算"}, {"预算", "0"}, {"预算", "-1"}, {"预算", "text"}, {"预算", "1.001"}, {"预算", "18", "extra"},
		{"忌口"}, {"忌口", " "}, {"已吃", "extra"}, {"换一个", "extra"}, {"历史", "extra"}, {"帮助", "extra"}, {"unknown"},
	} {
		if text := invoke(t, h, u, args...); !strings.Contains(text, "晚饭插件用法") {
			t.Fatalf("invalid %v: %q", args, text)
		}
		if got := invoke(t, h, u); got != first {
			t.Fatalf("invalid %v changed recommendation: %q", args, got)
		}
	}
	after, err := p.service.Profile(context.Background(), personal)
	if err != nil || after.BudgetCents != before.BudgetCents || len(after.ExcludedTags) != 0 {
		t.Fatalf("invalid input changed profile: %+v %v", after, err)
	}
	invoke(t, h, u, "已吃")
	invoke(t, h, u, "预算", "不限")
	invoke(t, h, u, "忌口", "牛肉")
	if text := invoke(t, h, u); !strings.Contains(text, "已吃") || !strings.Contains(text, "[a]") {
		t.Fatal(text)
	}
	if text := invoke(t, h, u, "帮助"); !strings.Contains(text, "当前预算：不限") || !strings.Contains(text, "当前忌口：牛肉") {
		t.Fatal(text)
	}
}

func TestPreferenceHandlerStorageAndSendFailures(t *testing.T) {
	p, h := setupPlugin(t, nil)
	storage := &testStorage{values: map[string][]byte{}}
	var err error
	p.service, err = decision.New(p.catalog, state.New(storage), time.Now, func() float64 { return 0 })
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("storage failure")
	u := &bot.User{ID: "pref"}
	for _, args := range [][]string{{"预算", "18.00"}, {"忌口", "牛肉"}, {"帮助"}} {
		storage.getErr = failure
		r := bot.NewNoopReply()
		e := &bot.Event{Platform: "mock", BotID: "bot", Sender: u, Command: &bot.Command{Name: "dinner", Args: args}}
		if err := h(context.Background(), e, r); !errors.Is(err, failure) || r.PlainText() != "" {
			t.Fatalf("read failure reported success: %q %v", r.PlainText(), err)
		}
		storage.getErr = nil
		if args[0] == "帮助" {
			continue
		}
		storage.setErr = failure
		if err := h(context.Background(), e, r); !errors.Is(err, failure) || r.PlainText() != "" || len(storage.values) != 0 {
			t.Fatalf("save failure reported success: %q %v", r.PlainText(), err)
		}
		storage.setErr = nil
	}
	// Invalid input returns usage without attempting to read or write personal state.
	storage.getErr, storage.setErr = failure, failure
	invoke(t, h, u, "预算", "1.001")
	storage.getErr, storage.setErr = nil, nil
	e := &bot.Event{Platform: "mock", BotID: "bot", Sender: u, Command: &bot.Command{Name: "dinner", Args: []string{"预算", "18.00"}}}
	r := &failedReply{Reply: bot.NewNoopReply(), err: errors.New("send failure")}
	if err := h(context.Background(), e, r); !errors.Is(err, r.err) {
		t.Fatal(err)
	}
	if text := invoke(t, h, u, "帮助"); !strings.Contains(text, "当前预算：18.00 元") {
		t.Fatalf("saved preference lost after failed send: %s", text)
	}
	for _, data := range storage.values {
		var saved decision.UserState
		if err := json.Unmarshal(data, &saved); err != nil || saved.Profile.BudgetCents != 1800 {
			t.Fatalf("saved state = %+v %v", saved, err)
		}
	}
}
