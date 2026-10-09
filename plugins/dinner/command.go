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
	pollScope, ok := pollScopeFor(e)
	if !ok {
		return r.Text("无法识别会话，请使用有效群聊或私聊发送命令。").Send(ctx)
	}
	if len(args) == 0 {
		view, err := p.polls.List(ctx, pollScope, scope, nil)
		return replyPoll(ctx, r, view, err, "")
	}
	if len(args) == 1 {
		if cents, valid := parseBudget(args[0]); valid {
			view, err := p.polls.List(ctx, pollScope, scope, &cents)
			return replyPoll(ctx, r, view, err, "")
		}
	}
	if len(args) == 1 && args[0] == "帮助" {
		profile, err := p.polls.Profile(ctx, pollScope)
		if err != nil {
			return err
		}
		return r.Text(helpText() + "\n\n" + formatProfile(profile)).Send(ctx)
	}
	if len(args) > 0 && args[0] == "投票" {
		if len(args) != 2 {
			return r.Text(helpText()).Send(ctx)
		}
		number, valid := parseVoteNumber(args[1])
		if !valid {
			return r.Text(helpText()).Send(ctx)
		}
		view, err := p.polls.Vote(ctx, pollScope, e.Sender.ID, number)
		return replyPoll(ctx, r, view, err, "投票已更新。\n")
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
		profile, err := p.polls.SetExcludedTags(ctx, pollScope, tags)
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
	if len(args) == 1 && (args[0] == "推荐" || args[0] == "换一个") {
		profile, profileErr := p.polls.Profile(ctx, pollScope)
		if profileErr != nil {
			return profileErr
		}
		if args[0] == "推荐" {
			result, err = p.service.RecommendFor(ctx, scope, profile)
		} else {
			result, err = p.service.RerollFor(ctx, scope, profile)
		}
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

func pollScopeFor(e *bot.Event) (decision.PollScope, bool) {
	kind := bot.MessagePrivate
	if e.Channel != nil && e.Channel.Kind != "" {
		kind = e.Channel.Kind
	} else if e.Message != nil && e.Message.Kind != "" {
		kind = e.Message.Kind
	}
	scope := decision.PollScope{Platform: e.Platform, BotID: e.BotID, Kind: string(kind), ID: e.Sender.ID}
	if kind == bot.MessageGroup || kind == bot.MessageChannel {
		if e.Channel == nil || strings.TrimSpace(e.Channel.ID) == "" {
			return scope, false
		}
		scope.ID = e.Channel.ID
	} else if kind != bot.MessagePrivate {
		return scope, false
	}
	return scope, true
}

func replyPoll(ctx context.Context, r bot.Reply, view decision.PollView, err error, prefix string) error {
	if err != nil {
		if errors.Is(err, decision.ErrNoCandidates) || errors.Is(err, decision.ErrNoPoll) || errors.Is(err, decision.ErrInvalidVote) || errors.Is(err, decision.ErrPollOptionUnavailable) {
			return r.Text(err.Error()).Send(ctx)
		}
		return err
	}
	return r.Text(prefix + formatPoll(view)).Send(ctx)
}

func parseVoteNumber(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(text)
	return number, err == nil && number > 0
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
