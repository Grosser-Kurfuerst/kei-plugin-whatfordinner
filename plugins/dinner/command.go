package dinner

import (
	"context"
	"errors"
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
	var result decision.Recommendation
	var err error
	if len(args) == 0 {
		result, err = p.service.Recommend(ctx, scope)
	} else if len(args) == 1 && args[0] == "换一个" {
		result, err = p.service.Reroll(ctx, scope)
	} else {
		return r.Text(helpText()).Send(ctx)
	}
	if err != nil {
		if errors.Is(err, decision.ErrNoCandidates) || errors.Is(err, decision.ErrRoundExhausted) || errors.Is(err, decision.ErrCurrentUnavailable) {
			return r.Text(err.Error()).Send(ctx)
		}
		return err
	}
	return r.Text(formatRecommendation(result)).Send(ctx)
}
