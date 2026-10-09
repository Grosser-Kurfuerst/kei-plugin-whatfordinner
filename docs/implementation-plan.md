# 晚饭决策插件分阶段实现方案

本方案依据 [system-design.md](./system-design.md)，说明具体实现顺序、文件改动、需求对应关系和验收标准。当前工作区只有设计文档，以下代码文件均为待创建文件；本文不代表实现已经完成。

## 1. 实现原则与阶段划分

沿用系统设计的四个模块：插件入口、候选清单、决策服务、状态存储。采用个人决策、共享清单、命令触发、单进程和 SQLite，不增加定时任务、群投票、外部餐饮接口或管理后台。

每阶段交付一条可使用的功能链路，包含所需的数据读取、业务处理和命令回复。每阶段完成后，都能通过同一个宿主程序启动，并通过 Mock 适配器验证已经交付的命令。

| 阶段 | 可运行的交付结果 | 本阶段支持的命令 |
|---|---|---|
| 1：启动与独立清单 | 机器人启动，读取、校验和展示共享候选清单 | `/dinner` 显示帮助；`候选`、`帮助` |
| 2：持久化每日决策 | 个人当日结果稳定，支持换选与重启恢复 | 默认命令改为推荐；增加 `换一个` |
| 3：实际用餐与减少重复 | 可以记录已吃、查看历史，近期记录影响推荐概率 | 增加 `已吃`、`历史`；推荐升级为七天加权抽选 |
| 4：偏好与完整验收 | 预算、忌口参与筛选，达到系统设计全部验收要求 | 增加 `预算`、`忌口`；帮助显示当前设置 |

阶段 2 的等权抽选是阶段 3 加权抽选的基础：使用同一个累计权重选择函数，最初给所有候选权重 1，随后接入历史权重计算。不引入两套抽选实现或运行模式开关。

## 2. 需求编号与实现位置

| 编号 | 需求 | 主要实现位置 | 完成阶段 |
|---|---|---|---|
| R1 | 作为 kei 插件接入，使用统一消息和纯文本回复 | `cmd/bot`、`plugins/dinner` | 1 |
| R2 | 清单维护独立；配置修改、停用和删除重启生效 | `internal/catalog` | 1；阶段 2、3 补充对当前结果与历史的影响 |
| R3 | 从有效候选中决定今晚吃什么 | `internal/decision` | 2 |
| R4 | 当日结果稳定；换选避开本轮选项；耗尽时保留结果 | `internal/decision`、`internal/state` | 2 |
| R5 | 个人数据隔离、同用户跨群共享、并发一致和重启恢复 | `plugins/dinner`、`internal/decision`、`internal/state` | 2；之后对新字段持续验证 |
| R6 | 推荐与实际用餐分开；一天一条已吃记录和历史快照 | `internal/decision`、`internal/state` | 3 |
| R7 | 近期吃得越近、次数越多，选择概率越低 | `internal/decision/selector.go` | 3 |
| R8 | 预算和忌口筛选，设置持久化；不自动放宽条件 | `plugins/dinner`、`internal/decision` | 4 |
| R9 | 无效输入不改状态；存储错误不报成功；回复失败可再次查询 | `plugins/dinner`、`internal/decision`、`internal/state` | 随相关功能交付，阶段 4 全量验收 |

后文的 A1～A12 对应系统设计第 8 节的验收编号。“部分覆盖”表示该阶段只验收已交付的字段和功能，不能据此宣称整条最终标准已经通过。

## 3. 文件结构与依赖约定

最终目录按实际职责组织，不增加通用框架层：

```text
whatfordinner/
├── go.mod
├── go.sum
├── cmd/bot/main.go
├── config.yaml
├── plugins/dinner/
│   ├── plugin.go
│   ├── command.go
│   ├── format.go
│   └── plugin_test.go
├── internal/catalog/
│   ├── catalog.go
│   └── catalog_test.go
├── internal/decision/
│   ├── types.go
│   ├── service.go
│   ├── selector.go
│   ├── service_test.go
│   └── selector_test.go
├── internal/state/
│   ├── store.go
│   └── store_test.go
└── docs/
    ├── system-design.md
    └── implementation-plan.md
```

- 宿主只负责信号、配置路径和 `kei.Run`，通过 `kei.Options.Plugins` 注入 dinner 实例；不同时再用 `init()` 注册这个插件。
- 插件入口调用公开 SDK `pkg/bot`，通过自己的模块完成业务；不依赖 kei 的 `internal/*` 或平台 SDK。
- `decision` 定义状态类型及 `Store` 接口；`state` 导入这些类型实现接口。`decision` 不导入 `state`。
- 清单使用具体的 `Catalog`；抽选使用普通函数。只抽象已有的存储边界，不增加策略注册表、仓储基类或事件总线。
- 实例状态保存在实例上。决策服务采用一把实例级互斥锁保护状态操作，发送消息放在锁外。

### 3.1 业务入口与可测试性

决策服务按阶段提供以下方法，具体参数类型使用系统设计的数据结构：

| 方法 | 职责 | 引入阶段 |
|---|---|---|
| `Recommend(ctx, scope)` | 返回或创建今天的推荐 | 2 |
| `Reroll(ctx, scope)` | 排除本轮已推荐项后重新选择 | 2 |
| `MarkEaten(ctx, scope)` | 记录当前选择，重复调用不重复记录 | 3 |
| `History(ctx, scope)` | 返回窗口内最近 7 条已吃记录 | 3 |
| `Profile(ctx, scope)` | 查询当前预算和忌口 | 4 |
| `SetBudget(ctx, scope, cents)` | 保存预算并处理当日推荐状态 | 4 |
| `SetExcludedTags(ctx, scope, tags)` | 替换忌口列表并处理当日推荐状态 | 4 |

推荐结果使用包含 `OptionSnapshot` 的具体结构返回；阶段 3 增加是否已吃、近七天用餐次数等实际需要的展示数据。Handler 格式化已有数据，不自行查询数据库或计算重复权重。

服务构造时接收 `now func() time.Time` 和 `randomFloat func() float64`。生产分别传 `time.Now`、`math/rand/v2.Float64`，测试传固定函数；不增加时钟或随机数接口层，也不通过生产命令修改时间和随机种子。

### 3.2 状态字段分阶段增加

阶段 2 引入 `Scope`、`OptionSnapshot`、`DailyDecision` 和只含 `Daily` 的 `UserState`；阶段 3 增加 `MealRecord`、`History`、`Eaten`；阶段 4 增加 `Profile`。

各阶段新增的 JSON 字段使用零值作为正常初始值：没有历史、尚未已吃、不限预算、无忌口。先前阶段保存的 JSON 可由后续版本直接解码，无需数据库迁移或独立兼容分支。既有字段的名字、类型和存储键规则保持不变。

## 4. 所有阶段共用的运行与验证方法

### 4.1 环境与依赖

使用 Go 1.25+，启用 cgo，并具备 SQLite 驱动所需的 C 编译器。项目直接依赖 kei，业务代码优先使用标准库，不再直接引入 ORM、SQLite 驱动、命令框架或测试框架。

阶段 1 先初始化模块并固定框架版本：

```sh
go mod init example.com/whatfordinner
go get github.com/RandomLemon/kei@0430906b795a4ebe2dc38ceffa88a5b62e17ef0d
```

`example.com/whatfordinner` 是本地开发模块名；若实施时已有正式模块路径，就统一采用正式路径，不同时维护两套 import。上述初始化只执行一次。

创建阶段 1 的宿主和插件 Go 文件后，再整理依赖；后续阶段新增导入时也执行这一步：

```sh
go mod tidy
```

`config.yaml` 使用系统设计第 7 节的 Mock＋SQLite 示例，其中有两个启用候选。宿主提供 `-config` 参数，默认读取 `config.yaml`。所有阶段都使用以下启动命令：

```sh
CGO_ENABLED=1 go build -o /tmp/whatfordinner-bot ./cmd/bot
KEI_STORAGE_DSN=/tmp/whatfordinner-acceptance.sqlite /tmp/whatfordinner-bot -config config.yaml
```

`KEI_STORAGE_DSN` 由 kei 的配置装配读取，插件不直接读环境变量。验收数据库放在 `/tmp`；重启恢复测试必须沿用同一个路径。需要全新状态的用例使用另一个数据库路径，不删除正在使用的数据库。

### 4.2 Mock 消息验证

在另一个终端检查服务、注入消息并查看回复：

```sh
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
curl --fail --silent --show-error \
  -X POST http://127.0.0.1:18080/inject \
  -H 'Content-Type: application/json' \
  -d '{"text":"/dinner 候选","user_id":"u1","kind":"private"}'
curl --fail --silent --show-error http://127.0.0.1:18080/sent
```

后续阶段复用这组命令，替换 `text`。多用户用例改变 `user_id`；跨群用例设置 `kind: "group"` 和不同的 `channel_id`。每次请求省略 `id`，让 Mock 生成新的事件 ID，避免被事件去重影响。

注入接口的成功响应只表示入队。需要等待 `/sent` 出现新增记录，检查 `Err` 为空，并读取 `Request.Message.Segments` 中的文本。手工联调可以再次查询；自动化检查应在明确的期限内轮询，例如最多 5 秒，超时直接失败，不以固定睡眠后的单次查询认定成功。

### 4.3 每阶段的退出条件

每阶段都必须：

1. 执行该阶段验收表中的自动化测试和 Mock 场景，记录实际结果。
2. 构建并启动宿主，验证本阶段所有支持的命令；未支持的子命令返回当前阶段的帮助，不返回虚假的成功。
3. 重跑此前阶段仍适用的验收用例。阶段 2 将默认命令从帮助改为推荐，对应断言按新契约更新；显式帮助、清单查询等已交付能力继续保留。
4. 通过格式、构建、静态检查、普通测试和竞态测试：

```sh
gofmt -l .
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go vet ./...
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go test -race ./...
```

`gofmt -l` 必须无输出，其他命令必须成功。业务测试断言推荐 ID、状态变化和消息结果，不绑定全部提示文案；使用标准库 `testing`，Handler 测试使用 SDK 的 `RecordingRegistrar`、`NoopReply`。

## 5. 阶段 1：宿主启动与独立候选清单

### 5.1 交付行为

机器人能够启动，`/dinner` 和 `/dinner 帮助` 展示本阶段用法，`/dinner 候选` 展示配置中的清单。该阶段先交付清单查询，不生成推荐，也不写个人业务状态。

### 5.2 具体改动点

| 文件 | 改动内容 | 对应需求 |
|---|---|---|
| `go.mod`、`go.sum` | 初始化模块，固定 kei 提交，整理依赖 | R1 |
| `cmd/bot/main.go` | 解析 `-config`；处理终止信号；导入 Mock；注入一个插件实例并调用 `kei.Run` | R1 |
| `config.yaml` | 按系统设计配置 Mock、SQLite 和两个示例候选 | R1、R2 |
| `internal/catalog/catalog.go` | 实现 `Option`、`Catalog`、`New`、`List`、`Find`；校验配置；保留配置顺序；查询返回副本 | R2 |
| `plugins/dinner/plugin.go` | 实现 `Metadata/Setup/Start/Stop`；配置结构使用 JSON 标签；在 `Setup` 构建清单、注册 `dinner` | R1、R2 |
| `plugins/dinner/command.go` | 解析 `候选`、`帮助`，校验调用者身份，其他子命令给出用法 | R1、R9 |
| `plugins/dinner/format.go` | 格式化清单和帮助；区分未知价格与已知价格，显示停用状态 | R1、R2 |
| `internal/catalog/catalog_test.go`、`plugins/dinner/plugin_test.go` | 覆盖清单校验、只读边界、注册后的可观察命令行为 | R1、R2、R9 |

清单模块只接收解码后的候选，不能读取文件、用户状态或平台事件。价格指针、标签切片等可变字段必须复制，不能因调用方修改查询结果而改变清单。

插件元信息名称使用 `dinner`，声明 `PermStorage`，为后续阶段使用框架存储准备。当前 `Setup` 只保存实际需要的依赖；没有后台任务，`Start/Stop` 返回 `nil`。不保存生命周期方法的临时 `ctx`。

### 5.3 验收标准

| 场景 | 明确通过标准 | 方式／最终验收对应 |
|---|---|---|
| 启动与命令链路 | `/healthz` 成功；帮助和候选命令各产生一条成功发送记录 | Mock 联调；R1 |
| 展示清单 | 两个示例候选可见；停用项带停用标记；空清单有明确提示 | Handler 单测＋Mock |
| 无效配置 | 重复 ID、空名称、负价格导致启动失败，错误定位到相关候选 | 清单单测＋启动联调；A1 |
| 修改清单 | 修改名称或价格、增加候选、停用或删除后，重启查询显示新清单 | 配置重启联调；A1 |
| 模块边界 | 修改 `List/Find` 返回值中的价格和标签不影响后续查询 | 清单单测；R2 |
| 错误输入 | 未知子命令只显示用法；身份缺失不创建匿名状态 | Handler 单测；A12 部分 |

本阶段运行验证：启动宿主后依次发送 `/dinner`、`/dinner 候选`、`/dinner 帮助`，检查发送记录。阶段结束时有实际可用的清单查询功能。

## 6. 阶段 2：持久化的当日推荐与换选

### 6.1 交付行为

`/dinner` 开始生成推荐，并在当天保持当前结果。`/dinner 换一个` 避开本轮已推荐项。结果属于个人，SQLite 重启后继续保留。本阶段只筛选启用状态，候选采用等权抽选。

### 6.2 具体改动点

| 文件 | 改动内容 | 对应需求 |
|---|---|---|
| `internal/decision/types.go` | 定义 `Scope`、`OptionSnapshot`、`DailyDecision`、`UserState`、`Store` 和推荐结果类型 | R3、R4、R5 |
| `internal/state/store.go` | 包装 `bot.Storage`；实现稳定的键生成、JSON `Load/Save`、TTL=0；仅不存在时返回默认状态 | R5、R9 |
| `internal/decision/selector.go` | 实现累计权重选择函数；接收候选及权重和随机值；本阶段统一权重为 1 | R3 |
| `internal/decision/service.go` | 实现 `Recommend/Reroll`；上海日期；状态加载、跨日重置、当前项校验、本轮排除、保存；统一互斥锁 | R3、R4、R5 |
| `plugins/dinner/plugin.go` | 从 `PluginContext` 获取 Storage；创建状态存储和决策服务 | R1、R5 |
| `plugins/dinner/command.go` | 将默认命令与 `换一个` 转给服务；从平台、BotID、Sender.ID 构造 Scope，不包含群 ID | R3、R4、R5 |
| `plugins/dinner/format.go` | 展示推荐名称、可选地点和参考价格；展示无候选、本轮耗尽、当前项失效等业务结果 | R3、R4、R9 |
| `internal/state/store_test.go`、`internal/decision/*_test.go`、`plugins/dinner/plugin_test.go` | 增加状态读写契约、区间选择、当日状态、并发和回复行为测试 | R3、R4、R5、R9 |

存储键严格沿用系统设计：`dinner:state:`＋固定字段 Scope JSON 的 SHA-256 十六进制值。`state` 不直接打开 SQLite，由宿主配置决定后端。

### 6.3 服务执行顺序

```text
校验命令和身份
  → 加锁
  → Load(scope)
  → 计算今天，必要时重置 Daily
  → 普通查询已有有效结果：返回该 ID 的最新清单信息
  → 否则：筛选启用项、排除 TriedIDs、等权选择
  → 更新 Current 与 TriedIDs
  → Save(scope, state)
  → 解锁
  → 发送回复
```

没有当前结果时，“换一个”与首次推荐使用相同选择流程。本轮耗尽保留结果；当前候选被删除或停用时，普通查询提示失效，用户显式换选后才改变结果。读写失败返回错误，不继续抽选、不返回成功。

### 6.4 验收标准

| 场景 | 明确通过标准 | 方式／最终验收对应 |
|---|---|---|
| 有效推荐 | 只选择启用项；空清单或全部停用时不生成结果 | 固定随机值单测＋Mock；A2 |
| 区间选择 | 两项权重均为 1 时，随机值小于 0.5 选第一项，等于或大于 0.5 选第二项 | 选择函数单测；A6 基础 |
| 当日稳定 | 同用户连续普通查询返回同一个 ID；重启后 ID 仍相同 | 服务单测＋SQLite 重启；A4、A11 部分 |
| 换选与耗尽 | 两个启用候选下，第一次换选得到另一项；再次换选提示耗尽，当前项不变 | 服务单测＋Mock；A4 |
| 跨日 | 上海本地跨零点后清空本轮状态，下次查询可以重新选到前一天的候选 | 固定时钟单测；A9 部分 |
| 清单变更 | 当前项停用或删除后提示失效；仅修改名称或价格且仍可用时，显示最新信息而不重新抽选 | 服务单测＋配置重启；A1、A12 部分 |
| 数据归属 | u1 与 u2 状态隔离；u1 在 g1、g2 和私聊中共享同一个当日结果 | 服务单测＋Mock；A10 部分 |
| 并发与失败 | 同一 Scope 并发普通查询只产生一个结果；Load/Save 失败不返回推荐成功；回复失败后普通查询仍可返回已保存结果 | 服务／Handler 测试＋竞态测试；A10、A12 部分 |

本阶段运行验证：对 u1 依次发送默认命令、普通重复查询、换一个、再次换一个；保留同一数据库路径重启，验证最后一次有效结果仍在。再用 u2 和两个群验证归属规则。

## 7. 阶段 3：实际用餐记录与七天降权

### 7.1 交付行为

用户可以标记“已吃”和查询历史。只有已吃记录影响权重；普通推荐和换选不增加历史。推荐使用系统设计的七天降权规则。

### 7.2 具体改动点

| 文件 | 改动内容 | 对应需求 |
|---|---|---|
| `internal/decision/types.go` | 增加 `MealRecord`、`UserState.History`、`DailyDecision.Eaten`；推荐结果增加必要的用餐状态展示数据 | R6、R7 |
| `internal/decision/service.go` | 实现 `MarkEaten/History`；加载后清理 30 天窗口；已吃幂等；已吃后拒绝换选；调用历史权重计算 | R4、R6、R7 |
| `internal/decision/selector.go` | 增加七天权重计算；复用阶段 2 的累计权重选择函数 | R7 |
| `plugins/dinner/command.go` | 接通 `已吃`、`历史` | R6 |
| `plugins/dinner/format.go` | 展示已吃状态、快照历史和真实的近期信息；已吃后的价格和名称使用历史快照 | R6、R7 |
| `internal/decision/*_test.go`、`internal/state/store_test.go`、`plugins/dinner/plugin_test.go` | 增加实际用餐、权重、窗口、幂等并发和新字段持久化测试；验证阶段 2 JSON 仍可读取 | R5、R6、R7、R9 |

`state/store.go` 沿用整个状态 JSON 的读写，不新增历史表、第二个键或日志流水。新增状态字段通过已有编解码流程保存。

### 7.3 关键实现规则

1. `MarkEaten` 加锁后加载并整理日期；没有今天的当前项时拒绝记录。已经已吃则直接返回现有已吃结果，不重复追加。
2. 首次标记时确认候选仍有效，从当前清单构造快照，同时更新 `Current`、追加 `MealRecord` 和设置 `Eaten=true`，一次 Save 完成。
3. 今天已经已吃时，普通查询返回保存的快照，换选返回明确提示；当前清单后来删除该项也不影响历史展示。
4. 用 `AddDate` 生成本地日历日期窗口：保留今天及此前 29 天；计权只使用昨天至此前第七天。不用秒差除以 24 小时推导日历天数。
5. 用候选 ID 汇总惩罚 `Σ(8-d)`，计算 `1/(1+penalty)`。不把 TriedIDs 当已吃历史，不完全禁用近期吃过的候选。
6. 换选先排除本轮项，然后计算剩余候选的权重和总权重。近期信息由服务提供，Handler 不因看到“已推荐”就声称用户吃过。

### 7.4 验收标准

| 场景 | 明确通过标准 | 方式／最终验收对应 |
|---|---|---|
| 推荐与用餐分开 | 多次推荐和换选不增加 History；首次已吃增加一条，并设置 Eaten | 状态转换单测；A7 |
| 幂等与并发 | 同一天重复或并发标记已吃，最终恰好一条记录 | 服务测试＋竞态测试；A7、A10 |
| 已吃后的行为 | 默认查询返回已吃快照；换选被拒绝；次日重置当前状态但保留历史 | 固定时钟单测＋Mock；A8 部分、A9 |
| 快照独立 | 已吃后修改、删除候选，历史仍显示当时名称、地点和价格 | 服务单测＋配置重启；R6 |
| 精确降权 | 无记录、昨天、三天前、七天前、昨天＋三天前的权重依次为 1、1/8、1/6、1/2、1/13；八天前不计权 | 纯函数单测；A5 |
| 加权区间 | 权重为 1 与 1/8 时，以 8/9 为选择边界；换选排除某项后重算区间 | 固定随机值测试；A6 |
| 历史窗口 | 今天及此前 29 天记录保留；第 30 天之前记录不展示；历史按日期倒序最多显示 7 条 | 固定时钟单测；A9 |
| 保存完整性 | 标记已吃保存失败，不返回成功；再次加载仍未已吃且未新增历史；成功后重启仍有记录 | 故障 Store 测试＋SQLite 重启；A11、A12 部分 |
| 跨阶段状态 | 阶段 2 的 JSON 解码后默认未已吃、历史为空，现有当日结果仍可继续查询和记录 | 存储测试；R5 |

权重验证必须使用固定记录和随机值。手工连续抽签的次数分布不能代替 A5、A6 的算法验收。

本阶段运行验证：用一个新数据库，依次执行推荐、已吃、再次已吃、历史、换一个；检查只记录一次，换选被拒绝。沿用数据库重启后，当前已吃状态和历史仍在。跨日及精确概率用自动化测试完成，不等待真实时间流逝。

## 8. 阶段 4：预算、忌口与全量验收

### 8.1 交付行为

实现系统设计中的全部命令。预算和忌口作为必须满足的条件；近期历史继续作为概率权重，二者分别处理。

### 8.2 具体改动点

| 文件 | 改动内容 | 对应需求 |
|---|---|---|
| `internal/decision/types.go` | 增加 `Profile` 和 `UserState.Profile`，默认预算 0、忌口空 | R8 |
| `internal/decision/service.go` | 增加 Profile 查询和设置；统一候选有效性判断；推荐、当前项检查、已吃记录共用规则；偏好变更按已吃状态处理 | R3、R4、R6、R8 |
| `plugins/dinner/command.go` | 解析预算金额及“不限”；解析忌口列表及“清空”；校验参数后调用服务 | R8、R9 |
| `plugins/dinner/format.go` | 帮助显示当前设置；展示条件不匹配和设置结果；只展示有依据的推荐理由 | R8、R9 |
| `internal/decision/service_test.go`、`internal/state/store_test.go`、`plugins/dinner/plugin_test.go` | 补齐筛选、偏好变更、边界输入、持久化和最终命令组合测试 | R5、R8、R9 |

不新增模块。筛选在决策服务内使用一个共享函数，权重函数保持原样。预算按十进制字符解析成分，金额须大于 0、最多两位小数；“不限”转换为预算 0。忌口按标签精确匹配，设置操作替换列表而不是追加。

尚未已吃时，成功修改偏好会清空 Current 和 TriedIDs，下次推荐使用新条件；已经已吃时只修改 Profile，保留已吃状态，未来的推荐使用新条件。

### 8.3 验收标准

| 场景 | 明确通过标准 | 方式／最终验收对应 |
|---|---|---|
| 预算边界 | 示例牛肉面 1800 分、盖浇饭 2000 分；预算 18.00 时只可选牛肉面；预算 17.99 时无匹配项 | 服务单测＋Mock；A3 |
| 未知价格 | 不限预算时可选；限预算时排除，不猜测价格 | 服务单测；A3 |
| 忌口 | 命中任一排除标签的候选不参与选择；清空后恢复参与；列表设置采用替换语义 | 服务单测＋Mock；A3 |
| 输入校验 | 0、负数、非金额文本、超过两位小数、缺少参数等不修改状态；“不限”清除预算 | 命令解析和 Handler 测试；A12 |
| 偏好变更 | 未已吃时清空当日推荐和本轮记录；已吃时保留当日记录，次日用新条件推荐 | 固定时钟服务测试；A8 |
| 清单与预算联动 | 当前项改价后超预算或标签变更后命中忌口，普通查询提示失效；已吃快照仍保留 | 服务测试＋配置重启；A12 |
| 持久化与旧状态 | 设置重启后仍在；阶段 3 JSON 默认不限预算、无忌口，历史和已吃状态不变 | 存储测试＋SQLite 重启；A11 |
| 最终覆盖 | 系统设计 A1～A12 均有通过记录；全部命令经 Mock 运行；此前阶段用例和全部质量检查通过 | 全量验收 |

本阶段运行验证：新用户先设置预算 18.00，推荐必须是牛肉面；设置忌口“牛肉”后不能选任何示例项；清空忌口后恢复牛肉面；标记已吃后改为不限预算，当日仍显示已吃牛肉面。重启后检查预算、忌口、已吃状态和历史。

## 9. 最终验收覆盖与交付记录

| 系统设计验收 | 覆盖位置 |
|---|---|
| A1 清单校验与维护 | 阶段 1 清单；阶段 2 当前项变更；阶段 3 历史快照 |
| A2 正常推荐 | 阶段 2，之后持续回归 |
| A3 硬条件 | 阶段 4 |
| A4 稳定与换选 | 阶段 2，阶段 3／4 补充已吃和偏好影响 |
| A5 近期降权 | 阶段 3 |
| A6 加权选择 | 阶段 2 选择函数；阶段 3 历史权重及重算 |
| A7 推荐与已吃分离 | 阶段 3 |
| A8 已吃后行为 | 阶段 3 已吃状态；阶段 4 偏好变更 |
| A9 日期与历史 | 阶段 2 跨日；阶段 3 日期窗口 |
| A10 隔离与并发 | 阶段 2 个人状态；阶段 3 并发已吃；全程竞态检查 |
| A11 重启持久化 | 阶段 2 当日结果；阶段 3 已吃历史；阶段 4 偏好 |
| A12 常见失败 | 各阶段相关失败用例；阶段 4 全量回归 |

实施时每阶段追加一条简短的验收记录到本文末尾，写明实现版本、启动命令、自动化检查结果、Mock 场景结果以及未通过项。未通过本阶段退出条件，不标记完成。当前阶段状态统一为**待实现**。

运行中的 SQLite 文件、临时二进制和测试输出不属于交付源码；遵守项目实际忽略规则，不随代码提交，也不为本方案自行修改 `.gitignore`。

## 10. 框架实现依据

本方案固定使用 kei 提交 `0430906b795a4ebe2dc38ceffa88a5b62e17ef0d`，相关接口和验证方式已按源码核对：

- [插件生命周期与权限](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/pkg/bot/plugin.go#L13-L60)。
- [PluginContext 和 Storage](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/pkg/bot/api.go#L20-L74)。
- [Config.Unmarshal 使用 JSON 标签](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/pkg/bot/api.go#L225-L239)。
- [宿主配置和插件实例注入](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/pkg/kei/kei.go#L38-L54)。
- [注入插件与 enabled 配置的关系](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/pkg/kei/assemble.go#L61-L89)。
- [Storage DSN 环境覆盖](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/internal/config/env.go#L91-L96)。
- [Mock 注入字段及 HTTP 接口](https://github.com/RandomLemon/kei/blob/0430906b795a4ebe2dc38ceffa88a5b62e17ef0d/adapters/mock/mock.go#L314-L385)。
