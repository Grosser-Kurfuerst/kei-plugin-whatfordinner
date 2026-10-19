// Package catalog 负责共享候选清单的校验和只读查询。
package catalog

import (
	"fmt"
	"slices"
	"strings"
)

// Option 是一个可直接选择的用餐选项；价格单位为分，nil 表示未知。
type Option struct {
	ID         string   `json:"id" yaml:"id"`
	Name       string   `json:"name" yaml:"name"`
	Venue      string   `json:"venue" yaml:"venue"`
	PriceCents *int64   `json:"price_cents" yaml:"price_cents"`
	Tags       []string `json:"tags" yaml:"tags"`
	Enabled    bool     `json:"enabled" yaml:"enabled"`
}

// Catalog 保存按配置顺序排列的只读清单。
type Catalog struct {
	items []Option
	index map[string]int
}

// New 校验并复制候选配置，允许空清单。
func New(items []Option) (*Catalog, error) {
	c := &Catalog{index: make(map[string]int, len(items))}
	for i, item := range items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Name) == "" {
			return nil, fmt.Errorf("catalog.items[%d]: id 和 name 不能为空", i)
		}
		if strings.ContainsAny(item.ID, " \t\r\n") {
			return nil, fmt.Errorf("catalog.items[%d]: id 不能包含空白字符", i)
		}
		if _, exists := c.index[item.ID]; exists {
			return nil, fmt.Errorf("catalog.items[%d]: id %q 重复", i, item.ID)
		}
		if item.PriceCents != nil && *item.PriceCents < 0 {
			return nil, fmt.Errorf("catalog.items[%d] (%s): price_cents 不能为负", i, item.ID)
		}
		item = clone(item)
		tags := make([]string, 0, len(item.Tags))
		seen := make(map[string]bool)
		for _, tag := range item.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				return nil, fmt.Errorf("catalog.items[%d] (%s): 标签不能为空", i, item.ID)
			}
			if !seen[tag] {
				tags = append(tags, tag)
				seen[tag] = true
			}
		}
		item.Tags = tags
		c.index[item.ID] = len(c.items)
		c.items = append(c.items, item)
	}
	return c, nil
}

// HasTag 报告清单中是否有任一选项使用该标签。
func (c *Catalog) HasTag(tag string) bool {
	for _, item := range c.items {
		if slices.Contains(item.Tags, tag) {
			return true
		}
	}
	return false
}

// List 返回清单副本，包含停用项。
func (c *Catalog) List() []Option {
	items := make([]Option, len(c.items))
	for i, item := range c.items {
		items[i] = clone(item)
	}
	return items
}

// Find 按稳定 ID 查找并返回副本。
func (c *Catalog) Find(id string) (Option, bool) {
	i, ok := c.index[id]
	if !ok {
		return Option{}, false
	}
	return clone(c.items[i]), true
}

func clone(item Option) Option {
	item.Tags = append([]string(nil), item.Tags...)
	if item.PriceCents != nil {
		price := *item.PriceCents
		item.PriceCents = &price
	}
	return item
}
