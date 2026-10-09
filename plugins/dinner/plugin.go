// Package dinner 将晚饭决策接入 kei 的统一命令接口。
package dinner

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"example.com/whatfordinner/internal/catalog"
	"example.com/whatfordinner/internal/decision"
	"example.com/whatfordinner/internal/state"
	"github.com/RandomLemon/kei/pkg/bot"
)

// Plugin 是独立的晚饭插件实例。
type Plugin struct {
	catalog *catalog.Catalog
	service *decision.Service
}

var _ bot.Plugin = (*Plugin)(nil)

// Metadata 返回插件名称和所需存储权限。
func (*Plugin) Metadata() bot.Metadata {
	return bot.Metadata{Name: "dinner", Version: "v0.1.0", Description: "晚饭候选清单与个人决策", Permissions: []bot.Permission{bot.PermStorage}}
}

// Setup 从框架配置构建清单并注册命令。
func (p *Plugin) Setup(ctx context.Context, reg bot.Registrar) error {
	pc, ok := bot.PluginContextFrom(ctx)
	if !ok {
		return fmt.Errorf("dinner: missing plugin context")
	}
	var config struct {
		Catalog struct {
			Items []catalog.Option `json:"items"`
		} `json:"catalog"`
	}
	if err := pc.Config.Unmarshal(&config); err != nil {
		return fmt.Errorf("dinner config: %w", err)
	}
	c, err := catalog.New(config.Catalog.Items)
	if err != nil {
		return err
	}
	p.catalog = c
	if pc.Storage == nil {
		return fmt.Errorf("dinner: missing storage")
	}
	service, err := decision.New(c, state.New(pc.Storage), time.Now, rand.Float64)
	if err != nil {
		return fmt.Errorf("dinner service: %w", err)
	}
	p.service = service
	reg.OnCommand("dinner", p.handle, bot.WithID("dinner:command"))
	return nil
}

// Start 无后台任务。
func (*Plugin) Start(context.Context) error { return nil }

// Stop 无插件独占资源。
func (*Plugin) Stop(context.Context) error { return nil }
