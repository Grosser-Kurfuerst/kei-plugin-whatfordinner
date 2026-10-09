package dinner

import (
	"example.com/whatfordinner/internal/catalog"
	"fmt"
	"strings"
)

func helpText() string {
	return "晚饭插件用法：\n/dinner 候选：查看共享清单\n/dinner 帮助：查看用法"
}

func money(cents *int64) string {
	if cents == nil {
		return "价格未知"
	}
	return fmt.Sprintf("参考价格 %d.%02d 元", *cents/100, *cents%100)
}

func formatCatalog(items []catalog.Option) string {
	if len(items) == 0 {
		return "候选清单为空，请维护者添加候选。"
	}
	var b strings.Builder
	b.WriteString("晚饭候选：")
	for _, item := range items {
		fmt.Fprintf(&b, "\n%s [%s]", item.Name, item.ID)
		if item.Venue != "" {
			fmt.Fprintf(&b, "，%s", item.Venue)
		}
		fmt.Fprintf(&b, "，%s", money(item.PriceCents))
		if len(item.Tags) > 0 {
			fmt.Fprintf(&b, "，标签：%s", strings.Join(item.Tags, "、"))
		}
		if !item.Enabled {
			b.WriteString("（停用）")
		}
	}
	return b.String()
}
