package dinner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"example.com/whatfordinner/internal/catalog"
	"github.com/RandomLemon/kei/pkg/bot"
)

func TestAdminCommandRuleIsAdminOnly(t *testing.T) {
	_, reg, _ := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, ok := reg.Command("dinner-admin")
	if !ok {
		t.Fatal("missing dinner-admin command")
	}
	if !rule.AdminOnly || rule.ID != "dinner:admin" {
		t.Fatalf("admin rule = %+v", rule)
	}
}

func TestAdminHelp(t *testing.T) {
	p, reg, _ := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, ok := reg.Command("dinner-admin")
	if !ok {
		t.Fatal("missing dinner-admin command")
	}
	before := p.service.ListCatalog()
	for _, raw := range []string{"", "帮助", "删除 x", "unknown"} {
		text := invokeAdmin(t, rule.Handler, raw)
		if !strings.Contains(text, "清单管理") || !strings.Contains(text, "添加") {
			t.Fatalf("help = %q", text)
		}
		if strings.Contains(text, "/dinner-admin 删除") {
			t.Fatalf("phase one help advertises delete: %q", text)
		}
	}
	if after := p.service.ListCatalog(); !reflect.DeepEqual(before, after) {
		t.Fatalf("help changed catalog: %+v -> %+v", before, after)
	}
}

func TestAdminAddImmediately(t *testing.T) {
	p, reg, storage := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, _ := reg.Command("dinner-admin")
	dinnerRule, _ := reg.Command("dinner")

	text := invokeAdmin(t, rule.Handler, "添加 id=c | name=新餐厅 | price=25.5 | venue=某路 1 号 | tags=川菜,点餐")
	for _, want := range []string{"已添加餐厅", "新餐厅 [c]", "25.50", "川菜"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}

	items := p.service.ListCatalog()
	option := findTestOption(t, items, "c")
	if option.PriceCents == nil || *option.PriceCents != 2550 {
		t.Fatalf("price = %v", option.PriceCents)
	}
	if !reflect.DeepEqual(option.Tags, []string{"川菜", "点餐"}) {
		t.Fatalf("tags = %#v", option.Tags)
	}
	stored, err := catalog.NewRepository(storage).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findTestOption(t, stored, "c")

	candidates := invoke(t, dinnerRule.Handler, &bot.User{ID: "u1"}, "候选")
	if !strings.Contains(candidates, "新餐厅 [c]") {
		t.Fatalf("candidate list did not update: %q", candidates)
	}
}

func TestAdminAddValidation(t *testing.T) {
	p, reg, storage := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, _ := reg.Command("dinner-admin")
	before, err := catalog.NewRepository(storage).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtimeBefore := p.service.ListCatalog()
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "添加 id=a | name=B", want: "已存在"},
		{raw: "添加 id=x", want: "id 和 name 必填"},
		{raw: "添加 id=x | name=X | price=0", want: "price 必须是正数"},
		{raw: "添加 id=x | name=X | price=不限", want: "price 必须是正数"},
		{raw: "添加 id=x | name=X | tags=不存在", want: "未知标签"},
		{raw: "添加 id=x | name=X | tags=川菜,,点餐", want: "标签不能为空"},
		{raw: "添加 id=x | name=X | foo=bar", want: "未知字段"},
		{raw: "添加 id=x | name=X | id=y", want: "重复"},
		{raw: "添加 id=x | name", want: "字段格式应为 key=value"},
		{raw: "添加 id=x | name=X |", want: "字段不能为空"},
		{raw: "添加 id=x y | name=X", want: "id 不能包含空白字符"},
	}
	for _, tc := range cases {
		if text := invokeAdmin(t, rule.Handler, tc.raw); !strings.Contains(text, tc.want) {
			t.Fatalf("%q: missing %q in %q", tc.raw, tc.want, text)
		}
		if got := p.service.ListCatalog(); !reflect.DeepEqual(got, runtimeBefore) {
			t.Fatalf("%q changed runtime catalog: %+v -> %+v", tc.raw, runtimeBefore, got)
		}
		stored, err := catalog.NewRepository(storage).Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored, before) {
			t.Fatalf("%q changed stored catalog: %+v -> %+v", tc.raw, before, stored)
		}
	}
}

func TestAdminAddSaveFailureLeavesRuntimeUnchanged(t *testing.T) {
	p, reg, storage := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, _ := reg.Command("dinner-admin")
	before := p.service.ListCatalog()
	failure := errors.New("save failed")
	storage.setErr = failure

	text, err := invokeAdminResult(t, rule.Handler, "添加 id=c | name=C")
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want %v", err, failure)
	}
	if text != "" {
		t.Fatalf("false success: %q", text)
	}
	if after := p.service.ListCatalog(); !reflect.DeepEqual(before, after) {
		t.Fatalf("runtime catalog changed: %+v -> %+v", before, after)
	}
	stored, err := catalog.NewRepository(storage).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, before) {
		t.Fatalf("stored catalog changed: %+v -> %+v", before, stored)
	}
}

func TestAdminAddPersistsAcrossRestart(t *testing.T) {
	_, reg, storage := setupPluginWithReg(t, []any{map[string]any{"id": "a", "name": "A", "enabled": true}}, testClock)
	rule, _ := reg.Command("dinner-admin")
	invokeAdmin(t, rule.Handler, "添加 id=c | name=C")

	restarted := setupPluginWithStorage(t, filepath.Join(t.TempDir(), "missing.yaml"), storage)
	findTestOption(t, restarted.service.ListCatalog(), "c")
}

func invokeAdmin(t *testing.T, h bot.Handler, raw string) string {
	t.Helper()
	text, err := invokeAdminResult(t, h, raw)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func invokeAdminResult(t *testing.T, h bot.Handler, raw string) (string, error) {
	t.Helper()
	r := bot.NewNoopReply()
	e := &bot.Event{
		Platform: "mock",
		BotID:    "bot",
		Sender:   &bot.User{ID: "u1"},
		Command:  &bot.Command{Name: "dinner-admin", Raw: raw, Args: strings.Fields(raw)},
	}
	err := h(context.Background(), e, r)
	return r.PlainText(), err
}

func findTestOption(t *testing.T, items []catalog.Option, id string) catalog.Option {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing option %q in %#v", id, items)
	return catalog.Option{}
}
