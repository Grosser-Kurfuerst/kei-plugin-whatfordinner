package dinner

import (
	"example.com/whatfordinner/internal/catalog"
	"example.com/whatfordinner/internal/decision"
	"fmt"
	"strings"
)

func helpText() string {
	return "晚饭插件用法：\n/dinner：查看今日推荐列表和票数（最多 5 项）\n/dinner 25：按每人 25 元预算生成列表（正数，最多两位小数）\n/dinner 不限：不限预算的列表\n/dinner 投票 1：投给编号 1，每人一票，可改投\n/dinner 推荐：获取个人随机推荐\n/dinner 换一个：换一个个人候选\n/dinner 已吃：记录个人推荐为实际用餐\n/dinner 历史：查看个人最近已吃记录\n/dinner 候选：查看共享清单\n/dinner 忌口 辣 花生：替换当前会话忌口\n/dinner 忌口 清空：清除忌口\n/dinner 帮助：查看用法和当前设置\n同群共享列表，私聊使用个人列表；改变预算或忌口会清空本轮票数，跨上海零点开启新一轮。"
}

func formatPoll(view decision.PollView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "今日推荐列表（%s，%d 人投票）：\n%s", view.Date, view.Voters, formatProfile(view.Profile))
	for _, entry := range view.Entries {
		fmt.Fprintf(&b, "\n%d. %s [%s] — %d 票", entry.Number, entry.Option.Name, entry.Option.ID, entry.Votes)
		if entry.Option.Venue != "" {
			fmt.Fprintf(&b, "\n   地点：%s", entry.Option.Venue)
		}
		fmt.Fprintf(&b, "\n   %s", money(entry.Option.PriceCents))
		if !entry.Available {
			b.WriteString("（当前不可投票）")
		}
	}
	b.WriteString("\n发送 /dinner 投票 编号；同一编号重复投票不加票，改投会转移原票。")
	return b.String()
}

func formatProfile(profile decision.Profile) string {
	budget := "不限"
	if profile.BudgetCents > 0 {
		budget = fmt.Sprintf("%d.%02d 元", profile.BudgetCents/100, profile.BudgetCents%100)
	}
	tags := "无"
	if len(profile.ExcludedTags) > 0 {
		tags = strings.Join(profile.ExcludedTags, "、")
	}
	return "当前预算：" + budget + "\n当前忌口：" + tags
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
