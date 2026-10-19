# 餐厅清单管理命令实施计划

## Context

为 dinner 插件增加受管理员保护的餐厅清单管理命令，本轮只支持**单个餐厅的添加和删除**，不提供编辑、批量操作、启用/停用切换或 seed 文件写入。

命令写入 `dinner:catalog` 后必须**立即生效**：当前进程中的 `/dinner 候选`、下一次投票列表生成和个人推荐都使用新清单，不需要重启。删除只从清单中移除餐厅，不改写个人状态、已吃历史、当日列表或选票；被删项在已有列表中标记为不可用。

权限复用 kei 的 `Auth` 中间件：命令规则使用 `bot.WithAdmin()`，管理员由宿主配置的 `auth.admin_users` 决定。未配置管理员时命令不会进入 Handler。

预计改动 8 个左右文件、约 350–450 行：生产代码约 150–200 行，测试约 150–200 行，文档与配置约 20–30 行。

## Approach

### 1. 让运行时清单可在锁内替换

在 `internal/decision/service.go` 增加四个方法，全部使用现有 `Service.mu`：

```go
// SetCatalog 替换当前清单；调用方必须传入已校验的 Catalog。
func (s *Service) SetCatalog(c *catalog.Catalog)

// ListCatalog 返回清单副本，包含停用项。
func (s *Service) ListCatalog() []catalog.Option

// FindCatalogOption 按 id 返回副本。
func (s *Service) FindCatalogOption(id string) (catalog.Option, bool)

// HasCatalogTag 报告清单中是否有任一选项使用该标签。
func (s *Service) HasCatalogTag(tag string) bool
```

实现要求：

- `SetCatalog` 在 `s.mu.Lock()` 内直接把 `s.catalog = c`，不做 nil 判断；`c` 由调用方通过 `catalog.New` 和 `tags.validateOptions` 校验。
- `ListCatalog` 在锁内返回 `s.catalog.List()`；`Catalog.List` 已经返回深拷贝。
- `FindCatalogOption`、`HasCatalogTag` 分别在锁内调用 `s.catalog.Find`、`s.catalog.HasTag`。
- 现有 `choose`、`MarkEaten`、`MarkEatenOption`、`History`、`suggest`、`suggestShared` 已在 `s.mu` 内访问 `s.catalog`，不改它们的直接访问。

### 2. 消除 `PollService` 对个人 catalog 的无锁访问

`internal/decision/poll.go` 中以下访问改为 Service 方法，保持 `PollService.mu -> Service.mu` 的既有加锁顺序；`Service` 不回调 `PollService`，不会形成反向锁：

- `Vote`：`s.personal.catalog.Find(id)` → `s.personal.FindCatalogOption(id)`。
- `MarkEaten`：`s.personal.catalog.Find(option.ID)` → `s.personal.FindCatalogOption(option.ID)`。
- `SetExcludedTags`：`hasTag := s.personal.catalog.HasTag` → `hasTag := s.personal.HasCatalogTag`。
- `view`：`s.personal.catalog.Find(saved.ID)` → `s.personal.FindCatalogOption(saved.ID)`。

完成后 `grep -rn 'personal\.catalog' internal plugins` 不应再有结果。

### 3. 移除插件中的重复 catalog 指针

`plugins/dinner/plugin.go` 的 `Plugin` 结构改为：

```go
type Plugin struct {
    tags    *tagTaxonomy
    service *decision.Service
    polls   *decision.PollService
    repo    *catalog.Repository
    adminMu sync.Mutex
}
```

- 删除 `catalog *catalog.Catalog` 字段。
- `Setup` 中保留局部变量 `c` 用于构造 `decision.New(c, ...)`，不再写 `p.catalog = c`。
- `repo := catalog.NewRepository(pc.Storage)` 后保存 `p.repo = repo`。
- `Metadata` 的 `Permissions` 改为 `[]bot.Permission{bot.PermStorage, bot.PermAdmin}`。
- 注册命令：

```go
reg.OnCommand("dinner", p.handle, bot.WithID("dinner:command"))
reg.OnCommand("dinner-admin", p.handleAdmin, bot.WithAdmin(), bot.WithID("dinner:admin"))
```

- `plugins/dinner/command.go` 的 `/dinner 候选` 改为：

```go
return r.Text(formatCatalog(p.service.ListCatalog())).Send(ctx)
```

- 测试中原先读取 `p.catalog.List()` 的位置改为 `p.service.ListCatalog()`；`setupPluginAt` 构造可控时钟 Service 时，用 `catalog.New(p.service.ListCatalog())` 取得初始清单。最终 `grep -rn 'p\.catalog' .` 不应再有结果。

### 4. 管理命令解析与处理

新建 `plugins/dinner/admin_command.go`。全部命令使用 `e.Command.Raw`，因为 kei 的 `Args` 会按空白切分，无法保留名称和地点中的空格。

#### 命令语法

```text
/dinner-admin 添加 id=<id> | name=<名称> | price=<人均元> | venue=<地点> | tags=<标签1,标签2>
/dinner-admin 删除 <id>
/dinner-admin 帮助
```

- 子命令精确匹配中文 `添加`、`删除`、`帮助`；空参数或未知子命令返回帮助。
- 添加字段用 `|` 分隔，每个字段为 `key=value`，键名只接受小写 `id`、`name`、`price`、`venue`、`tags`。
- 字段值可包含空格，但不能再包含 `|`。
- `id`、`name` 必填；`id` 不能包含空白字符。
- `price` 可选，单位元；省略或 `price=` 表示价格未知。提供时必须满足 `parseBudget` 且结果大于 0，即正数、最多两位小数；`price=0`、负数、`不限` 均拒绝。
- `venue` 可选，保留内部空格。
- `tags` 可选，英文逗号分隔；去除首尾空白、按出现顺序去重；空标签（如 `tags=川菜,,点餐`）报错；每个标签必须满足 `p.tags.Has(tag)`。
- `enabled` 不是可接受字段；新餐厅固定 `Enabled: true`。

#### Handler 结构

```go
func (p *Plugin) handleAdmin(ctx context.Context, e *bot.Event, r bot.Reply) error
func (p *Plugin) addRestaurant(ctx context.Context, r bot.Reply, raw string) error
func (p *Plugin) deleteRestaurant(ctx context.Context, r bot.Reply, raw string) error
func splitAdminCommand(raw string) (sub, rest string)
func parseAdminFields(raw string) (map[string]string, error)
func parseAdminTags(raw string) ([]string, error)
func (p *Plugin) buildCatalog(items []catalog.Option) (*catalog.Catalog, error)
func adminHelpText() string
```

`handleAdmin`：

- `e == nil || e.Command == nil` 时回复帮助。
- `sub, rest := splitAdminCommand(e.Command.Raw)`；`""`、`帮助` 回复帮助；`添加` 调用 `addRestaurant`；`删除` 调用 `deleteRestaurant`；其他回复帮助。
- 如果 `p == nil || p.service == nil || p.repo == nil`，回复 `清单管理尚未初始化。` 并返回。

`splitAdminCommand`：`strings.TrimSpace` 后，用第一个空格或制表符切出子命令，剩余部分 `TrimSpace`；没有剩余时返回 `(raw, "")`。

`parseAdminFields`：

- 按 `|` 分割；每段 `TrimSpace` 后必须非空，否则返回 `字段不能为空`。
- 每段用第一个 `=` 切成 key/value，两侧 `TrimSpace`；没有 `=` 返回 `字段格式应为 key=value`。
- key 为空或不属于允许集合返回 `未知字段 "<key>"`。
- 同一 key 第二次出现返回 `字段 "<key>" 重复`。
- 返回 `map[string]string`；允许 value 为空，由添加逻辑区分必填/可选。

`parseAdminTags`：

- `strings.TrimSpace(raw) == ""` 返回 `nil, nil`。
- 按英文逗号分割，逐项 `TrimSpace`；任一项为空返回 `标签不能为空`。
- 按首次出现顺序去重后返回。

`buildCatalog`：

```go
c, err := catalog.New(items)
if err != nil { return nil, err }
if err := p.tags.validateOptions(c.List()); err != nil { return nil, err }
return c, nil
```

#### 添加流程

`addRestaurant` 先解析字段，再执行：

1. `id := strings.TrimSpace(fields["id"])`，`name := strings.TrimSpace(fields["name"])`；任一为空返回 `id 和 name 必填`。
2. `strings.ContainsAny(id, " \t\r\n")` 为真返回 `id 不能包含空白字符`。
3. `parseAdminTags(fields["tags"])`；逐项调用 `p.tags.Has`，不认识的返回 `未知标签 "<tag>"`。
4. 如果 `fields["price"]` 非空，调用 `parseBudget`；`!ok || cents <= 0` 返回 `price 必须是正数，最多两位小数`；否则 `price := &cents`。
5. `p.adminMu.Lock()`，`defer p.adminMu.Unlock()`。锁内调用 `p.service.ListCatalog()` 获取当前清单；已有同 `id` 返回 `id "<id>" 已存在`。
6. 构造 `catalog.Option{ID: id, Name: name, Venue: fields["venue"], PriceCents: price, Tags: tags, Enabled: true}`，追加到清单副本。
7. `c, err := p.buildCatalog(items)`；校验失败回复 `添加失败：<err>`。
8. `p.repo.Save(ctx, c.List())`；失败返回 `fmt.Errorf("保存清单: %w", err)`，不回复成功，也不更新运行时。
9. `p.service.SetCatalog(c)`。
10. 从 `c.Find(id)` 取规范化后的餐厅，回复：

```text
已添加餐厅：
<formatCatalogOption(option)>
下一次 /dinner 会使用新清单。
```

所有第 1–7 步的输入错误通过 `r.Text("添加失败：" + err.Error()).Send(ctx)` 返回；第 8 步返回原始存储错误；第 9 步后回复成功。

#### 删除流程

`deleteRestaurant`：

1. `id := strings.TrimSpace(raw)`；为空或含空白字符返回 `id 不能为空或包含空白字符`。
2. `p.adminMu.Lock()`，`defer p.adminMu.Unlock()`。
3. `items := p.service.ListCatalog()`；按 `items[i].ID == id` 查找。未找到回复 `删除失败：未找到 id "<id>"`。
4. 保存被删除项，`items = append(items[:i], items[i+1:]...)`。
5. `c, err := p.buildCatalog(items)`；失败回复 `删除失败：<err>`。
6. `p.repo.Save(ctx, c.List())`；失败返回 `fmt.Errorf("保存清单: %w", err)`，不更新运行时。
7. `p.service.SetCatalog(c)`。
8. 回复：

```text
已删除餐厅：
<formatCatalogOption(removed)>
已生成的今日列表和已吃历史保留；被删项会显示为不可用。
```

允许删除最后一家餐厅；清单为空时 `/dinner` 继续按现有 `ErrNoCandidates` 行为提示。

#### 帮助文本

`adminHelpText()` 返回固定文本：

```text
清单管理（仅管理员）：
/dinner-admin 添加 id=<id> | name=<名称> | price=<人均元> | venue=<地点> | tags=<标签1,标签2>
/dinner-admin 删除 <id>
/dinner-admin 帮助
id 和 name 必填；price、venue、tags 可选；省略 price 表示价格未知。
price 为正数，最多两位小数；tags 用英文逗号分隔；字段用 | 分隔，字段值不能包含 |。
```

### 5. 抽出餐厅行格式化

在 `plugins/dinner/format.go` 新增：

```go
func formatCatalogOption(item catalog.Option) string
```

输出与现有 `formatCatalog` 中单行一致：

```text
<name> [<id>]，<venue>，<money>，标签：<tag1>、<tag2>
```

- `venue` 为空时省略该段；标签为空时省略标签段；`!Enabled` 时追加 `（停用）`。
- `formatCatalog` 改为先写 `晚饭候选：`，然后对每项写 `"\n" + formatCatalogOption(item)`；现有输出保持不变。
- 管理命令成功回复使用 `formatCatalogOption`。

### 6. 配置与文档

- `config.yaml` 顶层增加本地 mock 管理员：

```yaml
auth:
  admin_users:
  - u1
```

  并在上方注释说明：本地试用使用 `u1`，生产环境替换为实际用户 ID；`auth.admin_users` 为空时 `/dinner-admin` 被 Auth 中间件拒绝。

- `README.md` 在“常用命令”后增加“管理餐厅清单”小节，包含：
  - 仅 `auth.admin_users` 中的用户可执行；
  - 添加/删除/帮助三条命令及字段规则；
  - 修改立即生效；
  - 删除只移除清单项，不改已吃历史、当日列表或选票；
  - 管理命令写入 `dinner:catalog`，不修改 `data/restaurants.yaml`；删除该存储键后重新导入 seed 会丢失管理命令改动。
- `docs/usage.md` 的“命令”表增加三行管理命令，并在表后增加一段权限、字段、立即生效和删除语义说明；开头概述补充“管理员清单维护命令”。把 `/dinner 候选` 的说明从“维护者配置的完整清单”改为“当前共享清单（可由管理员增删）”；把“当前清单共 19 家”改为“初始 seed 共 19 家”，避免管理命令增删后文档失真。
- 不修改 `docs/system-design.md`，该文件已标记为历史设计，当前行为以 `docs/usage.md` 为准。

## 分阶段迭代与验收

按以下顺序实施。每个阶段结束时都必须能够编译、通过阶段内测试，并可独立提交和回滚；后续阶段不依赖未合入的临时改动。阶段 1 和阶段 2 的验收命令使用：

```sh
gofmt -l .
go vet ./...
CGO_ENABLED=1 go test ./... -count=1
```

阶段 3 再增加 race、构建和端到端验证。

### 阶段 1：运行时清单同步 + 单餐厅添加

依赖：无。

交付：

- `internal/decision/service.go` 增加 `SetCatalog`、`ListCatalog`、`FindCatalogOption`、`HasCatalogTag`。
- `internal/decision/poll.go` 四处直接访问改为上述方法。
- `plugins/dinner/plugin.go` 移除 `catalog` 字段、保存 `repo`、注册带 `bot.WithAdmin()` 的 `dinner-admin`；Metadata 增加 `bot.PermAdmin`。
- `plugins/dinner/command.go` 的 `/dinner 候选` 改用 `p.service.ListCatalog()`。
- `plugins/dinner/admin_command.go` 实现帮助和 `添加` 子命令；本阶段的 `adminHelpText` 只列 `添加`、`帮助`，`删除` 暂未实现，任何未知子命令统一返回帮助且不修改数据。
- `plugins/dinner/format.go` 抽出 `formatCatalogOption`。
- `config.yaml` 增加 `auth.admin_users: [u1]`。
- `README.md`、`docs/usage.md` 增加权限和“添加”命令说明；删除说明留到阶段 2。
- 测试：`TestAdminCommandRuleIsAdminOnly`、`TestAdminHelp`、`TestAdminAddImmediately`、`TestAdminAddValidation`、`TestAdminAddSaveFailureLeavesRuntimeUnchanged`、`TestAdminAddPersistsAcrossRestart`。

验收条件：

1. `grep -rn 'p\.catalog' --include='*.go' .` 无结果，`grep -rn 'personal\.catalog' internal plugins` 无结果。
2. `gofmt -l .`、`go vet ./...`、`CGO_ENABLED=1 go test ./... -count=1` 全部通过；现有 `/dinner` 命令测试不回归。
3. 管理员添加合法餐厅：
   - 回复包含 `已添加餐厅`、新名称和新 id；
   - 同一进程立即调用 `/dinner 候选` 能看到新餐厅；
   - `catalog.NewRepository(storage).Load` 含新餐厅；
   - 使用同一 storage、缺失 seed 重新装配后，新餐厅仍在。
4. 表驱动输入——重复 id、缺少 name、`price=0`、未知标签、未知字段、重复字段、缺少等号、空字段——均返回对应错误，且运行时清单和仓库都不变。
5. `storage.setErr` 时添加返回错误，运行时清单和仓库都不变，不出现成功回复。
6. `dinner-admin` 规则满足 `AdminOnly == true`、`ID == "dinner:admin"`；手工用 `u2` 调用添加时没有成功回复。

### 阶段 2：单餐厅删除

依赖：阶段 1 的清单同步和添加命令已完成。

交付：

- `plugins/dinner/admin_command.go` 增加 `删除` 子命令和 `deleteRestaurant`。
- `adminHelpText` 更新为最终文本，加入删除语法。
- `README.md`、`docs/usage.md` 增加删除语义：只删清单项，不改个人状态、已吃历史、当日列表和选票；当前列表中的被删项显示为不可用。
- 测试：`TestAdminDeleteImmediately`、`TestAdminDeletePreservesHistoryAndMarksPollUnavailable`、`TestAdminDeleteValidation`、`TestAdminDeleteSaveFailureLeavesRuntimeUnchanged`、`TestAdminDeletePersistsAcrossRestart`。

验收条件：

1. 删除存在的餐厅后，回复包含 `已删除餐厅`；`p.service.ListCatalog()` 和仓库立即不含该 id，`/dinner 候选` 也不含该 id。
2. 使用同一 storage、缺失 seed 重新装配后，删除结果仍然生效。
3. 删除前已生成的当日列表仍保留该项，并在 `/dinner` 输出中显示 `当前不可用`；删除前的 `/dinner 历史` 记录仍能显示该餐厅。
4. `删除 missing`、空 id、含空白的 id 均返回对应错误，清单、仓库和历史不变。
5. `storage.setErr` 时删除返回错误，运行时清单和仓库仍保留该餐厅。
6. 删除最后一家餐厅成功；随后 `/dinner` 按现有 `ErrNoCandidates` 路径提示，不 panic、不写入损坏状态。
7. `gofmt -l .`、`go vet ./...`、`CGO_ENABLED=1 go test ./... -count=1` 全部通过；`TestAdminDelete*` 与阶段 1 测试全部通过。

### 阶段 3：端到端验收与文档一致性

依赖：阶段 1、阶段 2 均已合入。

交付：

- 不增加新功能；只修复端到端验收发现的问题，并完成 README、usage、config 与实际命令的一致性。
- 执行本文“端到端手动验证”的完整流程。

验收条件：

1. `gofmt -l .` 无输出；`go vet ./...`、`go build ./...`、`CGO_ENABLED=1 go test ./... -count=1`、`CGO_ENABLED=1 go test -race ./... -count=1` 全部通过。
2. 真实 Mock 宿主管理员 `u1` 添加餐厅后，`/sent` 出现成功回复，随后 `/dinner 候选` 立即出现新餐厅。
3. 非管理员 `u2` 执行添加没有成功回复，`/dinner 候选` 不出现 `blocked`。
4. 管理员删除餐厅后，`/sent` 出现成功回复，随后 `/dinner 候选` 不含该餐厅；重启同一 SQLite 后结果仍保持。
5. README、`docs/usage.md` 中的命令语法、权限说明、立即生效和删除语义与实际行为一致；`config.yaml` 的 `auth.admin_users` 与本地示例 `u1` 一致；所有本地 Markdown 链接可解析。
6. 没有遗留的临时调试代码、未使用字段或未实现占位分支。

## Critical files & anchors

- `internal/decision/service.go`：`Service` 定义与 `New`，新增四个 catalog 访问方法；所有访问在 `s.mu` 内。
- `internal/decision/poll.go`：`Vote`、`MarkEaten`、`SetExcludedTags`、`view` 中四处 `personal.catalog` 直接访问。
- `plugins/dinner/plugin.go`：`Plugin` 结构、`Setup`、命令注册、Metadata 权限；移除 `catalog` 字段并保存 `repo`。
- `plugins/dinner/command.go`：`/dinner 候选` 改为 `p.service.ListCatalog()`。
- `plugins/dinner/format.go`：`formatCatalog` 与新增 `formatCatalogOption`。
- `plugins/dinner/admin_command.go`：新文件，全部管理命令逻辑。
- `plugins/dinner/plugin_test.go`：`setupPluginAt` 拆出可返回 RecordingRegistrar 的 helper，更新 `p.catalog` 引用。
- `config.yaml`、`README.md`、`docs/usage.md`：权限与命令文档。

## Verification

### 自动测试

新增 `plugins/dinner/admin_command_test.go`，至少包含：

阶段 1：

1. `TestAdminCommandRuleIsAdminOnly`
   - 从 RecordingRegistrar 取 `dinner-admin` 规则，断言 `rule.AdminOnly == true`、`rule.ID == "dinner:admin"`。
2. `TestAdminHelp`
   - 调用空参数或 `帮助`，断言回复含 `清单管理` 和 `添加`；不修改 `p.service.ListCatalog()`。
3. `TestAdminAddImmediately`
   - 初始清单只有 `a`；执行：
     `添加 id=c | name=新餐厅 | price=25.5 | venue=某路 1 号 | tags=川菜,点餐`
   - 断言回复含 `已添加餐厅`、`新餐厅 [c]`、`25.50`、`川菜`。
   - 断言 `p.service.ListCatalog()` 和 `catalog.NewRepository(storage).Load` 都含 `c`，价格 `2550`，标签 `["川菜","点餐"]`。
   - 直接调用 dinner handler 的 `/dinner 候选`，断言含 `新餐厅 [c]`，证明立即生效。
4. `TestAdminAddValidation`
   - 表驱动断言以下输入均返回对应错误且 `p.service.ListCatalog()` 不变，仓库也不变：
     - 重复 id：`添加 id=a | name=B`
     - 缺少 name：`添加 id=x`
     - 非法价格：`添加 id=x | name=X | price=0`
     - 未知标签：`添加 id=x | name=X | tags=不存在`
     - 未知字段：`添加 id=x | name=X | foo=bar`
     - 重复字段：`添加 id=x | name=X | id=y`
     - 缺少等号：`添加 id=x | name`
     - 空字段：`添加 id=x | name=X |`
5. `TestAdminAddSaveFailureLeavesRuntimeUnchanged`
   - 设置 `storage.setErr = errors.New("save failed")`，执行添加；断言返回错误，`p.service.ListCatalog()` 不含新 id，仓库未变。
6. `TestAdminAddPersistsAcrossRestart`
   - 添加 `c` 后，用同一 storage 和缺失 seed 重新 `setupPluginWithStorage`；断言 `p.service.ListCatalog()` 含 `c`。

阶段 2：

7. `TestAdminDeleteImmediately`
   - 初始清单含 `a`、`b`；执行 `删除 a`，断言回复含 `已删除餐厅`；`p.service.ListCatalog()` 和仓库都不含 `a` 且仍含 `b`；`/dinner 候选` 不含 `a`。
8. `TestAdminDeletePreservesHistoryAndMarksPollUnavailable`
   - 初始清单只有 `a`；调用 dinner handler `/dinner` 生成列表，再 `/dinner 已吃 a` 写入历史。
   - 执行 `删除 a`；调用 `/dinner 历史` 断言仍含 `a`；调用 `/dinner` 断言列表项含 `当前不可用`。
9. `TestAdminDeleteValidation`
   - 表驱动断言 `删除 missing`、`删除`（空 id）、含空白的 id 均返回对应错误，且清单、仓库、历史均不变。
10. `TestAdminDeleteSaveFailureLeavesRuntimeUnchanged`
    - 设置 `storage.setErr`，执行删除；断言返回错误，运行时和仓库仍包含该餐厅。
11. `TestAdminDeletePersistsAcrossRestart`
    - 删除后使用同一 storage 和缺失 seed 重新装配；断言清单不含被删项。
12. `TestAdminDeleteLastRestaurant`
    - 初始清单只有 `a`；删除 `a` 后，`p.service.ListCatalog()` 为空，仓库为空；调用 dinner handler 返回的文本含 `暂无可选项`。

测试 helper 要求：

- 将现有 `setupPluginAt` 的主体提取为：

```go
func setupPluginWithReg(t *testing.T, items []any, now func() time.Time) (*Plugin, *bot.RecordingRegistrar, *testStorage)
```

  该 helper 完成 `Setup`、用 `catalog.New(p.service.ListCatalog())` 构造可控时钟 Service、返回 registrar。`setupPluginAt` 继续返回 `(*Plugin, bot.Handler, *testStorage)`，内部调用新 helper 并取 `reg.Command("dinner")`，现有调用点不用改。

- 新增 `invokeAdmin(t, h, raw)`：构造 `Command{Name: "dinner-admin", Raw: raw, Args: strings.Fields(raw)}`，Sender `u1`，返回 `NoopReply.PlainText()`。
- 现有测试中 `p.catalog.List()` 全部替换为 `p.service.ListCatalog()`。

执行：

```sh
gofmt -w internal/decision/service.go internal/decision/poll.go \
  plugins/dinner/admin_command.go plugins/dinner/command.go \
  plugins/dinner/format.go plugins/dinner/plugin.go \
  plugins/dinner/admin_command_test.go plugins/dinner/plugin_test.go
go vet ./...
CGO_ENABLED=1 go test ./... -count=1
CGO_ENABLED=1 go test -race ./... -count=1
go build ./...
git diff --check
```

### 端到端手动验证

使用临时 SQLite，启动真实 Mock 宿主：

```sh
tmp=$(mktemp -d)
CGO_ENABLED=1 go build -o "$tmp/bot" ./cmd/bot
KEI_STORAGE_DSN="$tmp/wfd.sqlite" "$tmp/bot" -config config.yaml &
pid=$!
# 等待 127.0.0.1:18080 可连接
```

1. 管理员添加：

```sh
curl -s http://127.0.0.1:18080/inject -H 'Content-Type: application/json' \
  -d '{"text":"/dinner-admin 添加 id=test-admin | name=测试餐厅 | price=25 | venue=测试路 1 号 | tags=川菜,点餐","user_id":"u1"}'
curl -s http://127.0.0.1:18080/sent
```

预期 `/sent` 出现 `已添加餐厅`、`测试餐厅 [test-admin]`。

2. 立即生效：

```sh
curl -s http://127.0.0.1:18080/inject -H 'Content-Type: application/json' \
  -d '{"text":"/dinner 候选","user_id":"u1"}'
curl -s http://127.0.0.1:18080/sent
```

预期 `/sent` 出现 `测试餐厅 [test-admin]`。

3. 非管理员被拒绝：

```sh
curl -s http://127.0.0.1:18080/inject -H 'Content-Type: application/json' \
  -d '{"text":"/dinner-admin 添加 id=blocked | name=Blocked","user_id":"u2"}'
curl -s http://127.0.0.1:18080/sent
```

预期没有 `已添加餐厅` 新回复；`/dinner 候选` 不含 `blocked`。

4. 删除：

```sh
curl -s http://127.0.0.1:18080/inject -H 'Content-Type: application/json' \
  -d '{"text":"/dinner-admin 删除 test-admin","user_id":"u1"}'
curl -s http://127.0.0.1:18080/sent
```

预期出现 `已删除餐厅`，随后 `/dinner 候选` 不含 `test-admin`。

5. 重启持久化：

```sh
kill $pid; wait $pid
KEI_STORAGE_DSN="$tmp/wfd.sqlite" "$tmp/bot" -config config.yaml &
pid=$!
# 重新请求 /dinner 候选
```

预期删除结果仍生效；再添加一家并重启，候选仍存在。结束后 `kill $pid`，删除 `$tmp`。

## Assumptions & contingencies

- 管理命令使用 kei 内置 `auth.admin_users`，不新增插件级管理员配置。若宿主没有配置 `auth.admin_users`，命令按预期不可用；这是故意的安全默认，不在插件内另做鉴权。
- 管理命令只修改 `dinner:catalog`，不修改 `data/restaurants.yaml`。如果之后手动删除 `dinner:catalog` 让插件重新导入 seed，管理命令的增删会被 seed 覆盖；文档必须明确这一点。
- `price=0` 视为无效，价格未知只能用省略 `price` 或 `price=` 表示。若执行中发现需要支持免费餐厅，再单独放宽解析并补测试。
