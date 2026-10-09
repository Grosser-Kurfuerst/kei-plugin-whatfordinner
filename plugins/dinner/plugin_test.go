package dinner

import (
	"context"
	"errors"
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
