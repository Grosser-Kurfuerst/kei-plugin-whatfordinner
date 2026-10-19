# 分阶段实现与验收记录

本文阶段 1～4 记录初始实现。2026-10-10 的命令与投票改动见文末，当前使用方法以 [使用说明](usage.md) 为准。

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

## 阶段 2：个人当日推荐与持久化

状态：已完成。前序提交：`0eb3c12`。

修改文件：新增 `internal/decision/types.go`、`service.go`、`selector.go` 及服务／区间测试，新增 `internal/state/store.go`、`store_test.go`；更新插件入口、命令、展示与 Handler 测试，以及本文。

实现：统一 Scope 和固定长度状态键；一份 JSON 持久化个人当日结果；实例互斥锁覆盖完整状态转换；支持当前结果、换选、耗尽与上海日期重置。抽选使用可控累计权重函数，当前权重均为 1。存储失败不报成功，发送失败不撤销已保存结果。

验证：五项质量检查全部通过；`python3 /tmp/whatfordinner-acceptance.py 2` 通过普通查询稳定、换选与耗尽、跨群共享、独立用户、实际 SQLite 重启恢复、配置改变时结果有效性和显式换选，以及阶段 1 配置／命令回归。固定时钟单测覆盖上海零点；30 个并发查询只创建一个当日结果。

评审：1 轮，最终 `NO_BLOCKING_ISSUES`；reviewer 独立执行 `go test -race -count=1 ./internal/decision ./internal/state ./plugins/dinner` 通过。无阻塞修复、拒绝 finding 或跳过主流程验证。

提交：`feat(decision): persist daily recommendations and rerolls`。

## 阶段 3：已吃记录与七天减少重复

状态：已完成。前序提交：`f93d808`。

修改文件：决策类型、服务、权重函数及测试，状态存储测试，插件命令、展示和 Handler 测试，以及本文。

实现：显式已吃记录；一次保存快照、历史和已吃状态；幂等记录及已吃后拒绝换选；30 天保留、倒序 7 条查询；只使用此前七天实际用餐的 `1/(1+Σ(8-d))` 权重，复用累计权重抽选。

验证：五项质量检查全部通过；`python3 /tmp/whatfordinner-acceptance.py 3` 通过实际已吃、重复已吃、历史、拒绝换选、SQLite 进程重启、删除或修改候选后的历史快照，以及此前阶段回归。固定测试验证全部权重例值、区间重算、窗口、30 个并发已吃只追加一条、保存失败不产生记录和阶段 2 JSON 正常升级。

评审：1 轮，最终 `NO_BLOCKING_ISSUES`；reviewer 独立运行普通测试及非缓存竞态测试通过。无阻塞修复、拒绝 finding 或跳过主流程验证。

提交：`feat(history): record meals and reduce recent dinner repeats`。

## 阶段 4：预算、忌口与完整验收

状态：已完成。前序提交：`8bfcc34`。

修改文件：`internal/decision/types.go`、`service.go`、`service_test.go`，`internal/state/store_test.go`，`plugins/dinner/command.go`、`format.go`、`plugin_test.go`，以及本文。

实现：增加个人 Profile，预算默认不限、忌口默认空；推荐、当前项检查和已吃记录共用启用状态／预算／标签条件。预算按十进制字符解析为整数分；忌口替换、去重和清空。未吃时设置偏好清空本轮；已吃时仅更新偏好，保留快照和历史。帮助读取当前设置；无效参数不读写个人状态，无匹配项不自动放宽条件。

验证：五项质量检查全部通过；`python3 /tmp/whatfordinner-acceptance.py 4` 通过全部命令、18.00／17.99 预算边界、忌口替换和清空、非默认预算与忌口的 SQLite 重启恢复、已吃后改偏好、改价与改标签导致当前失效，以及前三阶段仍适用的 Mock 场景。固定测试补齐未知价格、精确标签、上海跨零点后的新条件、偏好并发更新和保存失败、阶段 3 JSON 零值兼容。

评审：1 轮，最终 `NO_BLOCKING_ISSUES`；reviewer 独立运行 `go test -count=1 ./...` 和 `go test -race -count=1 ./internal/decision ./internal/state ./plugins/dinner` 通过，并核对全部验收证据。无阻塞修复、拒绝 finding 或跳过主流程验证。

提交：`feat(preferences): filter dinner choices by budget and excluded tags`。

## 最终验收对应记录

| 验收 | 已通过的证据 |
|---|---|
| A1 清单校验与维护 | `TestCatalogValidation`、`TestCatalogChangeAndNoCandidates`；实际配置新增、改名、改价、停用、删除及重复 ID 启动失败 |
| A2 正常推荐 | `TestDailyDecisionAndReroll`、`TestCommands`；实际 Mock 推荐展示名称、地点与参考价格 |
| A3 硬条件 | `TestBudgetAndExcludedTagsFilter`；实际预算 18.00／17.99 与牛肉忌口，无匹配项不放宽条件 |
| A4 稳定与换选 | `TestDailyDecisionAndReroll`、`TestPreferenceChangesRestartRound`；实际普通查询稳定、换选、耗尽和配置失效后的显式换选 |
| A5 近期降权 | `TestSevenDayWeights` 精确验证 1、1/8、1/6、1/2、1/13，以及当天和八天前不计权 |
| A6 加权选择 | `TestPickIntervals`、`TestWeightedServiceUsesOnlyMeals` 验证 8/9 边界与排除本轮项后的重新计权 |
| A7 推荐与已吃分离 | `TestMealRecordingAndSnapshot`、`TestWeightedServiceUsesOnlyMeals`；实际重复已吃只显示一条历史 |
| A8 已吃后行为 | `TestMealRecordingAndSnapshot`、`TestPreferencesPreserveMealAndSurviveMidnight`；实际拒绝换选、修改偏好及修改清单后仍显示已吃快照 |
| A9 日期与窗口 | `TestShanghaiDateBoundary`、`TestHistoryWindowAndLimit`、`TestPreferencesPreserveMealAndSurviveMidnight`；固定时钟验证上海零点、保留偏好、7 天计权与 30 天历史 |
| A10 隔离与并发 | `TestIsolationAndConcurrentRecommend`、`TestConcurrentAndFailedMealSave`、`TestConcurrentPreferenceUpdatesPreserveBothFields`；实际 u1／u2 隔离、g1／g2／私聊共享；竞态检查通过 |
| A11 重启持久化 | 实际沿用同一 SQLite 路径反复重启，验证当日结果、已吃状态与历史、非默认预算和忌口；状态编解码测试验证旧 JSON 可直接读取 |
| A12 常见失败 | 服务及 Handler 故障测试验证读写错误不报成功、无效参数不改状态、发送失败后已保存状态可查询；实际改价／改标签超出条件后拒绝推荐和记录已吃 |

业务验收以固定测试断言和实际 `/sent` 回复为准，Mock 注入的 202 响应只代表入队。跨日与概率通过固定时钟、随机值验证，不以等待一天或观察抽签频率代替。

## 2026-10-10：会话列表、投票与餐厅初始化

已按新需求实现：`/dinner`、`/dinner 25`、`/dinner 不限` 返回最多五项列表和票数；`/dinner 投票 编号` 返回更新后的列表；个人随机推荐改为 `/dinner 推荐`。同群当天共享，每人一票、可改投，私聊按用户独立。改变预算或忌口清空选票，上海跨日重开，同条件查询保持编号。

改动集中在 `internal/decision/poll.go`、`internal/state/poll.go` 和 `plugins/dinner/command.go`；现有个人服务共用候选筛选与历史降权。投票保存每个用户当前所选餐厅 ID，票数查询时汇总，整轮通过一次存储写入，未增加后台服务或外部 API。个人历史和投票分别持久化。

`config.yaml` 初始化 19 家公开地图餐厅，距鼓楼校区地图中心直线距离 0.450–3.640 km。来源、参考人均与覆盖限制见 [来源说明](restaurant-sources.md)，当前命令和本地操作见 [使用说明](usage.md)。

验证结果：

- `gofmt -l .` 无输出；构建 `cmd/bot` 成功；`go vet ./...`、`go test ./...`、`go test -race ./...` 全部通过。
- `internal/decision/poll_test.go` 覆盖稳定列表、同预算保留、五项加权不放回抽选、每人一票和改投、上海零点、会话隔离、30 人并发计票、存储失败和配置失效。
- `internal/state/poll_test.go` 覆盖完整 JSON 恢复、键隔离、个人状态不被覆盖、损坏 JSON 和存储错误。
- `plugins/dinner/poll_command_test.go` 覆盖多人群共享、群与私聊隔离、忌口与预算切换、无效参数不访问存储、消息类型识别、保存失败不报成功，以及发送失败后已保存的票仍可查询。
- `TestPersonalRecommendationUsesConversationProfile` 覆盖个人推荐使用会话条件、条件改变重开未吃轮次、已吃快照和历史保留。
- 当次使用临时脚本完成真实 Mock＋SQLite 联调：初始 19 项清单、100 元过滤与 25 元仅宜宾燃面可选、群成员同列表、投票／重复投票／改投、群和私聊隔离、同预算保票、条件改变清票，以及个人推荐／换一个／已吃／历史回归。该脚本未入库；仓库内的 `plugins/dinner/integration_test.go` 使用真实 Mock＋SQLite 覆盖投票与个人推荐的重启恢复。

本次修改已包含在 `0347588`（feat(dinner): 增加群投票与南京餐厅候选清单）。

## 2026-10-19：按 id 投票与群共享已吃历史

投票和已吃改为使用餐厅稳定 `id`，列表不再暴露可变的顺序编号。`/dinner 已吃 <id>` 可记录投票列表中的任意餐厅，并同时写入个人历史和会话共享历史；群或频道的下一轮列表使用共享历史计算七天降权，私聊列表继续使用个人历史。共享记录按用户、日期和餐厅去重，保留三十天。

验证：`gofmt -l .` 无输出；`go vet ./...`、`go test ./...`、`go test -race ./...`、`go build ./...` 全部通过。`internal/decision/poll_test.go` 覆盖 id 投票、非法 id、按 id 已吃、重试幂等、个人与共享历史写入、共享历史加权、会话条件校验和私聊隔离；`plugins/dinner/integration_test.go` 使用真实 Mock＋SQLite 覆盖 id 投票、已吃、历史查询及进程重启恢复。

上一轮先统一了忌口标签：`中餐厅`→`中餐`、`外国餐厅`→`外国菜`、`四川菜(川菜)`→`川菜`、`本家韩国料理`→`韩国料理`，补充父类标签并移除品牌标签；忌口输入去除首尾空白、去重并排序，标签顺序不再触发列表重开；清单中不存在的标签直接提示且不修改设置。标签词表、维度和新增规则记录在 [`docs/tags.md`](tags.md)。`TestPollExcludedTagsNormalized` 覆盖顺序、空白和未知标签，`TestCatalogReadOnlyAndOrder` 覆盖清单标签去空白与去重。

### 2026-10-19：四层标签配置与 `/dinner 标签`

标签词表改为 [`config.yaml`](../config.yaml) 的 `plugins.dinner.tag_groups`，固定 `大类`、`菜系`、`品类`、`就餐形式` 四层，共 45 个常用值；`catalog.items[].tags`（现为 [`data/restaurants.yaml`](../data/restaurants.yaml) 的 `items[].tags`）只能使用词表中的值，启动时校验，未知标签、重复层、空层或跨层重复都会使插件启动失败。餐厅标签同步补全 `点餐`/`自助餐`、品类和大类。

新增 `/dinner 标签`，按四层顺序展示当前支持的标签；`/dinner 忌口` 使用同一词表校验，词表内但暂无餐厅使用的标签也会被接受，便于未来扩展。`TestTagTaxonomy`、`TestTagTaxonomyValidation`、`TestTagTaxonomyValidatesCatalog`、`TestCatalogTagValidation`、`TestPollUsesConfiguredTagSet` 覆盖词表结构、启动校验、未来标签和命令展示。

### 2026-10-19：餐厅清单拆分为独立 seed 文件

餐厅清单从 `config.yaml` 移到 [`data/restaurants.yaml`](../data/restaurants.yaml)，配置只保留 `plugins.dinner.catalog_seed` 路径。插件启动时读取该 YAML 文件并交给 `catalog.New`；`catalog.Option` 增加 YAML 标签以复用同一结构。seed 文件是为首次接入 kei 统一存储准备的初始化数据，后续迁移方案见 [清单存储迁移](storage-migration-plan.md)。

验证：`gofmt -l .` 无输出；`go test ./...` 全部通过。测试夹具改为在临时目录生成 seed 文件，`TestSQLitePersistenceAcrossRestart` 继续覆盖真实 Mock＋SQLite 重启恢复。

### 2026-10-19：清单持久化到 `dinner:catalog`

新增 `internal/catalog/repository.go`，把完整清单保存为 kei 存储中的一个 JSON 文档（`version: 1`，TTL 0）。插件启动顺序改为：读取 `dinner:catalog`；仅在 `bot.ErrNotFound` 时读取 seed，完成结构及标签校验后再写入，避免无效清单落库。已有清单时不再读取 seed；seed 修改、存储损坏、未知版本等行为按 [迁移方案](storage-migration-plan.md) 处理。

验证：`internal/catalog/repository_test.go` 覆盖首次未找到、保存/读取、规范化、无效清单不落库、读取错误、损坏 JSON、未知版本、空清单、nil 存储和 `ErrNotFound` 映射；插件测试覆盖首次导入、重启复用、删除后重新导入、读取失败不回退 seed 和缺失存储；`TestCatalogStorageLifecycle` 使用真实 Mock＋SQLite 验证首次写入、修改 seed 不覆盖存储、删除键后重新导入。
