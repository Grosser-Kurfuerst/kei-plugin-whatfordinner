package dinner

import (
	"context"
	"fmt"
	"strings"

	"example.com/whatfordinner/internal/catalog"
	"github.com/RandomLemon/kei/pkg/bot"
)

func (p *Plugin) handleAdmin(ctx context.Context, e *bot.Event, r bot.Reply) error {
	if e == nil || e.Command == nil {
		return r.Text(adminHelpText()).Send(ctx)
	}
	sub, rest := splitAdminCommand(e.Command.Raw)
	if sub == "" || sub == "帮助" {
		return r.Text(adminHelpText()).Send(ctx)
	}
	if p == nil || p.service == nil || p.repo == nil {
		return r.Text("清单管理尚未初始化。").Send(ctx)
	}
	switch sub {
	case "添加":
		return p.addRestaurant(ctx, r, rest)
	default:
		return r.Text(adminHelpText()).Send(ctx)
	}
}

func (p *Plugin) addRestaurant(ctx context.Context, r bot.Reply, raw string) error {
	fields, err := parseAdminFields(raw)
	if err != nil {
		return r.Text("添加失败：" + err.Error()).Send(ctx)
	}
	id := strings.TrimSpace(fields["id"])
	name := strings.TrimSpace(fields["name"])
	if id == "" || name == "" {
		return r.Text("添加失败：id 和 name 必填").Send(ctx)
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return r.Text("添加失败：id 不能包含空白字符").Send(ctx)
	}
	tags, err := parseAdminTags(fields["tags"])
	if err != nil {
		return r.Text("添加失败：" + err.Error()).Send(ctx)
	}
	for _, tag := range tags {
		if !p.tags.Has(tag) {
			return r.Text(fmt.Sprintf("添加失败：未知标签 %q", tag)).Send(ctx)
		}
	}
	var price *int64
	if fields["price"] != "" {
		cents, ok := parseBudget(fields["price"])
		if !ok || cents <= 0 {
			return r.Text("添加失败：price 必须是正数，最多两位小数").Send(ctx)
		}
		price = &cents
	}

	p.adminMu.Lock()
	defer p.adminMu.Unlock()
	items := p.service.ListCatalog()
	for _, item := range items {
		if item.ID == id {
			return r.Text(fmt.Sprintf("添加失败：id %q 已存在", id)).Send(ctx)
		}
	}
	items = append(items, catalog.Option{
		ID:         id,
		Name:       name,
		Venue:      fields["venue"],
		PriceCents: price,
		Tags:       tags,
		Enabled:    true,
	})
	c, err := p.buildCatalog(items)
	if err != nil {
		return r.Text("添加失败：" + err.Error()).Send(ctx)
	}
	if err := p.repo.Save(ctx, c.List()); err != nil {
		return fmt.Errorf("保存清单: %w", err)
	}
	p.service.SetCatalog(c)
	option, _ := c.Find(id)
	return r.Text("已添加餐厅：\n" + formatCatalogOption(option) + "\n下一轮列表生成会使用新清单。").Send(ctx)
}

func splitAdminCommand(raw string) (sub, rest string) {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		return raw[:i], strings.TrimSpace(raw[i+1:])
	}
	return raw, ""
}

func parseAdminFields(raw string) (map[string]string, error) {
	allowed := map[string]bool{"id": true, "name": true, "price": true, "venue": true, "tags": true}
	fields := make(map[string]string)
	for _, part := range strings.Split(raw, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("字段不能为空")
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("字段格式应为 key=value")
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !allowed[key] {
			return nil, fmt.Errorf("未知字段 %q", key)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("字段 %q 重复", key)
		}
		fields[key] = value
	}
	return fields, nil
}

func parseAdminTags(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var tags []string
	seen := make(map[string]bool)
	for _, tag := range strings.Split(raw, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, fmt.Errorf("标签不能为空")
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	return tags, nil
}

func (p *Plugin) buildCatalog(items []catalog.Option) (*catalog.Catalog, error) {
	c, err := catalog.New(items)
	if err != nil {
		return nil, err
	}
	if err := p.tags.validateOptions(c.List()); err != nil {
		return nil, err
	}
	return c, nil
}

func adminHelpText() string {
	return `清单管理（仅管理员）：
/dinner-admin 添加 id=<id> | name=<名称> | price=<人均元> | venue=<地点> | tags=<标签1,标签2>
/dinner-admin 帮助
id 和 name 必填；price、venue、tags 可选；省略 price 表示价格未知。
price 为正数，最多两位小数；tags 用英文逗号分隔；字段用 | 分隔，字段值不能包含 |。`
}
