# whatfordinner

# 是啊，吃什么

[kei](https://github.com/RandomLemon/kei) 的晚饭插件：从餐厅清单生成今日推荐列表，支持预算、忌口、群投票和个人随机推荐。最近七天实际吃过的餐厅会降低被选中的概率。

## 快速启动

需要 Go 1.25.1 或更新版本，以及用于 SQLite 构建的 C 编译器。

```sh
git clone https://github.com/Grosser-Kurfuerst/whatfordinner.git
cd whatfordinner
KEI_STORAGE_DSN=/tmp/whatfordinner.sqlite CGO_ENABLED=1 go run ./cmd/bot -config config.yaml
```

内置启动程序已注册插件，默认使用 Mock 适配器，监听 `127.0.0.1:18080`。另开终端体验群投票：

```sh
curl -s http://127.0.0.1:18080/inject \
  -H 'Content-Type: application/json' \
  -d '{"text":"/dinner","user_id":"u1","kind":"group","channel_id":"g1"}'

curl -s http://127.0.0.1:18080/inject \
  -H 'Content-Type: application/json' \
  -d '{"text":"/dinner 投票 1","user_id":"u1","kind":"group","channel_id":"g1"}'

curl -s http://127.0.0.1:18080/sent
```

`/inject` 返回接收确认，机器人回复稍后写入 `/sent`；将 `user_id` 改为 `u2` 可体验多人投票。数据库路径可按需修改，使用相同路径重启会恢复当天列表和选票。

## 常用命令

| 命令 | 说明 |
|---|---|
| `/dinner` | 查看今日列表，最多五项，显示编号和票数 |
| `/dinner 25`、`/dinner 不限` | 设置人均预算或取消限制，并返回列表 |
| `/dinner 投票 1` | 投给编号 1，返回更新后的列表和票数 |
| `/dinner 推荐` | 获取个人随机推荐 |
| `/dinner 换一个` | 更换个人推荐 |
| `/dinner 已吃`、`/dinner 历史` | 记录实际用餐、查看个人历史 |
| `/dinner 忌口 火锅`、`/dinner 忌口 清空` | 设置或清除排除标签 |
| `/dinner 候选`、`/dinner 帮助` | 查看完整清单、命令帮助 |

同一群或频道当天共享列表、预算和忌口；私聊按用户独立。每人一票，可改投。修改预算或忌口会重开列表并清空选票，重复查询保留选票；每天按上海时区零点换日。投票不会自动记录为“已吃”。

## 接入已有 kei 程序

插件随宿主 Go 程序编译，由 kei 管理生命周期。当前模块名为 `example.com/whatfordinner`，在宿主项目中使用本地 `replace` 接入：

```sh
go mod edit -require=example.com/whatfordinner@v0.0.0
go mod edit -replace=example.com/whatfordinner=../whatfordinner
```

将 `../whatfordinner` 调整为插件仓库的实际位置。在宿主启动代码中导入插件，并在调用 `kei.Run(ctx, opts)` 前追加注册：

```go
import "example.com/whatfordinner/plugins/dinner"

// opts 为宿主已有的 kei.Options。
opts.Plugins = append(opts.Plugins, &dinner.Plugin{})
```

将 [config.yaml](config.yaml) 中完整的 `plugins.dinner` 配置合并到宿主配置，保留宿主的平台适配器和机器人配置。随后执行 `go mod tidy`，使用 `CGO_ENABLED=1` 构建并启动宿主。完整启动示例见 [cmd/bot/main.go](cmd/bot/main.go)。连接实际聊天平台时，宿主还需注册并配置对应适配器。

## 维护餐厅清单

编辑 [config.yaml](config.yaml) 的 `plugins.dinner.catalog.items` 后重启。餐厅 `id` 保持唯一、稳定；`price_cents` 单位为分，未知时省略；`enabled: false` 可停用。价格未知的餐厅只参与不限预算的推荐，忌口按标签精确匹配。

当前内置 19 家南大鼓楼校区周边候选餐厅，比格和流浪泡泡均使用鼓楼吾悦广场店。价格为公开来源参考值，地点及价格核验情况见 [餐厅来源说明](docs/restaurant-sources.md)。

更多说明：[完整使用指南](docs/usage.md) · [系统设计](docs/system-design.md) · [实现规划](docs/implementation-plan.md) · [实施记录](docs/implementation-results.md)
