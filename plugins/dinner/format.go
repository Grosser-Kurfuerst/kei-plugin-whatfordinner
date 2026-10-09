package dinner

import (
	"example.com/whatfordinner/internal/catalog"
	"example.com/whatfordinner/internal/decision"
	"fmt"
	"strings"
)

func helpText() string {
	return "晚饭插件用法：\n/dinner：查看今晚推荐\n/dinner 换一个：换一个候选\n/dinner 已吃：记录今天实际用餐\n/dinner 历史：查看最近已吃记录\n/dinner 候选：查看共享清单\n/dinner 帮助：查看用法"
}

func formatRecommendation(result decision.Recommendation) string {
	option := result.Option
	text := fmt.Sprintf("今晚推荐：%s [%s]", option.Name, option.ID)
	if result.Eaten {
		text = fmt.Sprintf("今天已吃并记录：%s [%s]", option.Name, option.ID)
	}
	if option.Venue != "" {
		text += "\n地点：" + option.Venue
	}
	text += "\n" + money(option.PriceCents)
	if !result.Eaten {
		if result.RecentMeals == 0 {
			text += "\n最近七天未吃过。"
		} else {
			text += fmt.Sprintf("\n此前七天吃过 %d 次，已降低抽选权重。", result.RecentMeals)
		}
	}
	return text
}

func formatHistory(history []decision.MealRecord) string {
	if len(history) == 0 {
		return "暂无已吃历史；推荐和换选不会自动记录用餐。"
	}
	var b strings.Builder
	b.WriteString("最近已吃历史：")
	for _, record := range history {
		fmt.Fprintf(&b, "\n%s %s [%s]", record.Date, record.Option.Name, record.Option.ID)
		if record.Option.Venue != "" {
			fmt.Fprintf(&b, "，%s", record.Option.Venue)
		}
		fmt.Fprintf(&b, "，%s", money(record.Option.PriceCents))
	}
	return b.String()
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
