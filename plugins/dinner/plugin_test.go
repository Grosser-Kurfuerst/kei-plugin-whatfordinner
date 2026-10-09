package dinner

import (
	"context"
	"github.com/RandomLemon/kei/pkg/bot"
	"strings"
	"testing"
)

func setupPlugin(t *testing.T, items []any) (*Plugin, bot.Handler) {
	t.Helper()
	p := &Plugin{}
	reg := bot.NewRecordingRegistrar()
	ctx := bot.WithPluginContext(context.Background(), bot.PluginContext{Config: bot.NewConfig(map[string]any{"catalog": map[string]any{"items": items}})})
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
	e := &bot.Event{Sender: sender, Command: &bot.Command{Name: "dinner", Args: args}}
	if err := h(context.Background(), e, r); err != nil {
		t.Fatal(err)
	}
	return r.PlainText()
}

func TestCommands(t *testing.T) {
	_, h := setupPlugin(t, []any{map[string]any{"id": "a", "name": "牛肉面", "enabled": false, "price_cents": 1800}, map[string]any{"id": "b", "name": "B", "enabled": true}})
	u := &bot.User{ID: "u1"}
	for _, args := range [][]string{nil, {"帮助"}, {"unknown"}, {"候选", "extra"}} {
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
