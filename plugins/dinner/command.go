package dinner

import (
	"context"
	"github.com/RandomLemon/kei/pkg/bot"
	"strings"
)

func (p *Plugin) handle(ctx context.Context, e *bot.Event, r bot.Reply) error {
	if e == nil || e.Sender == nil || strings.TrimSpace(e.Sender.ID) == "" {
		return r.Text("无法识别调用者，请使用有效用户身份发送命令。").Send(ctx)
	}
	var args []string
	if e.Command != nil {
		args = e.Command.Args
	}
	if len(args) == 1 && args[0] == "候选" {
		return r.Text(formatCatalog(p.catalog.List())).Send(ctx)
	}
	return r.Text(helpText()).Send(ctx)
}
