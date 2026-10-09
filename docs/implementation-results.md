# 分阶段实现与验收记录

## 原始请求与约束

用户显式调用 `implement-plan-iterations`，要求依据 `docs/implementation-plan.md` 实现。未限定阶段范围，按阶段 1～4 顺序完成；每阶段执行验证、只读 reviewer 评审及必要修复，完成后创建一次 Conventional Commits 提交。

参考材料为 `docs/implementation-plan.md` 和 `docs/system-design.md`。约束：中文文档、独立清单模块、个人状态、七天减少重复、单进程 SQLite、每阶段可运行、不扩展定时任务或外部服务、不修改 `.gitignore`。完整验收契约为系统设计 A1～A12。

开始时工作区没有 Git 仓库，只有上述两份用户已有文档。为按阶段提交初始化了仓库，已有文档保持原样和未跟踪状态；验收记录放在这个新增文件中，避免将既有文件内容混入阶段提交。本文是实施状态的记录。

## 迭代清单

| 阶段 | 目标与依赖 | 排除项 | 验收范围 |
|---|---|---|---|
| 1 | 宿主与独立清单，无前序依赖 | 不写个人状态、不生成推荐 | 清单校验、查询、配置重启、Mock 帮助链路 |
| 2 | 在阶段 1 上实现状态存储、当日推荐和换选 | 不记录已吃、不支持偏好 | 当日稳定、耗尽、跨日、隔离、并发、重启与失败 |
| 3 | 在阶段 2 上加入已吃、历史与七天降权 | 不支持预算和忌口设置 | 幂等、快照、窗口、精确权重、加权区间、重启 |
| 4 | 在阶段 3 上加入偏好及完整验收 | 不增加模块或兜底选择 | 系统设计 A1～A12 全部行为 |

每阶段均执行 `gofmt -l .`、`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`，使用 Go 1.25.1、`CGO_ENABLED=1`、固定 kei 提交 `0430906b795a4ebe2dc38ceffa88a5b62e17ef0d`。构建二进制及实际 SQLite 数据均放在 `/tmp`，不纳入提交。

自动化验证采用 SDK `RecordingRegistrar`、`NoopReply` 和测试存储替身；实际 Mock＋SQLite 联调由临时验收脚本启动进程、轮询回复、重新启动进程验证。脚本为执行工具，不作为应用依赖或生产兜底。

## 阶段 1：宿主与独立清单

状态：已完成。

修改文件：`go.mod`、`go.sum`、`config.yaml`、`cmd/bot/main.go`、`internal/catalog/catalog.go` 及其测试、`plugins/dinner/plugin.go`、`command.go`、`format.go`、`plugin_test.go`，以及本文。

实现：宿主注入 dinner 插件；配置由框架加载；清单独立校验并深复制；帮助和候选命令可用。没有后台任务或个人状态。

验证：五项质量检查全部通过；`python3 /tmp/whatfordinner-acceptance.py 1` 通过实际 Mock 启动、帮助、候选、无效子命令、配置名称／价格／新增／停用／删除与重启、重复 ID 启动失败。

运行：`CGO_ENABLED=1 go build -o /tmp/whatfordinner-bot ./cmd/bot`，再用 `KEI_STORAGE_DSN=/tmp/whatfordinner-acceptance.sqlite /tmp/whatfordinner-bot -config config.yaml` 启动。

评审：1 轮，最终 `NO_BLOCKING_ISSUES`；无阻塞修复、拒绝 finding 或跳过验证。内置 reviewer 角色因模型服务不可用未能启动，改由独立子智能体执行同样的专用只读评审，其自行运行 `go test ./...` 通过。

提交：`feat(catalog): add runnable bot and independent dinner catalog`。
