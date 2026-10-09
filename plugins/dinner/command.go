package dinner

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"example.com/whatfordinner/internal/decision"
	"github.com/RandomLemon/kei/pkg/bot"
)

func (p *Plugin) handle(ctx context.Context, e *bot.Event, r bot.Reply) error {
	if e == nil || e.Sender == nil || strings.TrimSpace(e.Sender.ID) == "" || e.Platform == "" || e.BotID == "" {
		return r.Text("无法识别调用者，请使用有效用户身份发送命令。").Send(ctx)
	}
	var args []string
	if e.Command != nil {
		args = e.Command.Args
	}
	if len(args) == 1 && args[0] == "候选" {
		return r.Text(formatCatalog(p.catalog.List())).Send(ctx)
	}
	scope := decision.Scope{Platform: e.Platform, BotID: e.BotID, UserID: e.Sender.ID}
	if len(args) == 1 && args[0] == "帮助" {
		profile, err := p.service.Profile(ctx, scope)
		if err != nil {
			return err
		}
		return r.Text(helpText() + "\n\n" + formatProfile(profile)).Send(ctx)
	}
	if len(args) > 0 && args[0] == "预算" {
		if len(args) != 2 {
			return r.Text(helpText()).Send(ctx)
		}
		cents, valid := parseBudget(args[1])
		if !valid {
			return r.Text(helpText()).Send(ctx)
		}
		profile, err := p.service.SetBudget(ctx, scope, cents)
		if err != nil {
			return err
		}
		return r.Text("预算已设置。\n" + formatProfile(profile)).Send(ctx)
	}
	if len(args) > 0 && args[0] == "忌口" {
		if len(args) < 2 {
			return r.Text(helpText()).Send(ctx)
		}
		tags := args[1:]
		for _, tag := range tags {
			if strings.TrimSpace(tag) == "" {
				return r.Text(helpText()).Send(ctx)
			}
		}
		if len(tags) == 1 && tags[0] == "清空" {
			tags = nil
		}
		profile, err := p.service.SetExcludedTags(ctx, scope, tags)
		if err != nil {
			return err
		}
		return r.Text("忌口已设置。\n" + formatProfile(profile)).Send(ctx)
	}
	if len(args) == 1 && args[0] == "历史" {
		history, err := p.service.History(ctx, scope)
		if err != nil {
			return err
		}
		return r.Text(formatHistory(history)).Send(ctx)
	}
	var result decision.Recommendation
	var err error
	if len(args) == 0 {
		result, err = p.service.Recommend(ctx, scope)
	} else if len(args) == 1 && args[0] == "换一个" {
		result, err = p.service.Reroll(ctx, scope)
	} else if len(args) == 1 && args[0] == "已吃" {
		result, err = p.service.MarkEaten(ctx, scope)
	} else {
		return r.Text(helpText()).Send(ctx)
	}
	if err != nil {
		if errors.Is(err, decision.ErrNoCandidates) || errors.Is(err, decision.ErrRoundExhausted) || errors.Is(err, decision.ErrCurrentUnavailable) || errors.Is(err, decision.ErrNoCurrent) || errors.Is(err, decision.ErrAlreadyEaten) {
			return r.Text(err.Error()).Send(ctx)
		}
		return err
	}
	return r.Text(formatRecommendation(result)).Send(ctx)
}

// parseBudget 将十进制元转换成整数分，不接受符号、指数或超过两位小数。
func parseBudget(text string) (int64, bool) {
	if text == "不限" {
		return 0, true
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, false
	}
	fraction := "00"
	if len(parts) == 2 {
		if len(parts[1]) < 1 || len(parts[1]) > 2 {
			return 0, false
		}
		fraction = parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
	}
	digits := parts[0] + fraction
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	cents, err := strconv.ParseInt(digits, 10, 64)
	return cents, err == nil && cents > 0
}
