# 清单存储迁移实施方案

## 1. 目标与范围

把餐厅清单从进程内静态配置迁移到 kei 统一存储，同时保留一份可审阅、可版本化的初始化 seed。

- 清单不再写在 `config.yaml`，改为独立文件 `data/restaurants.yaml`。
- 首次接入存储时，如果 `dinner:catalog` 不存在，读取 seed 并导入。
- 导入成功后，存储是唯一运行时来源；seed 只在 `dinner:catalog` 不存在时使用。
- 清单模块保持纯内存查询，不在每次推荐时访问存储。
- 第一阶段不新增管理命令，不支持运行时热更新；修改清单后通过删除存储键并重启完成重新导入。

## 2. 当前状态

| 项目 | 现状 |
|---|---|
| 配置 | `config.yaml` 只保留 `plugins.dinner.tag_groups` 与 `plugins.dinner.catalog_seed` |
| seed | [`data/restaurants.yaml`](../data/restaurants.yaml)，19 家餐厅，YAML |
| 启动 | `plugins/dinner/plugin.go` 先调用 `Repository.Load`；未找到时读取 seed，校验后保存，再构建静态 `*catalog.Catalog` |
| 清单模块 | `internal/catalog` 负责结构校验、只读查询；`repository.go` 负责 `dinner:catalog` 持久化 |
| 状态模块 | `internal/state` 通过 `bot.Storage` 保存个人状态和投票 |
| 测试 | `repository_test.go` 覆盖存储契约；插件测试覆盖首次导入、复用与重新导入；集成测试使用真实 Mock + SQLite |

清单拆分与存储导入均已实现。本方案未改变现有命令、推荐和投票行为。

## 3. 设计决策

### 3.1 数据来源

| 数据 | 来源 | 生命周期 |
|---|---|---|
| 标签词表 | `config.yaml` 的 `plugins.dinner.tag_groups` | 启动时校验，失败即退出 |
| 餐厅 seed | `data/restaurants.yaml`，路径由 `catalog_seed` 指定 | 仅在 `dinner:catalog` 不存在时读取 |
| 运行时清单 | kei `Storage` 的 `dinner:catalog` | 首次导入后长期使用 |
| 个人状态、投票 | kei `Storage` 的 `dinner:state:*`、`dinner:poll:*` | 保持现状 |

`catalog_seed` 为相对路径时按进程工作目录解析；宿主从其他目录启动时应配置绝对路径。

### 3.2 存储格式

键：`dinner:catalog`，TTL 为 `0`（不过期）。

值：

```json
{
  "version": 1,
  "items": [
    {
      "id": "stable-restaurant-id",
      "name": "餐厅名称",
      "venue": "门店地址",
      "price_cents": 2500,
      "tags": ["火锅", "点餐"],
      "enabled": true
    }
  ]
}
```

`version` 用于未来结构变更。当前只接受 `1`；未知版本直接报错，不静默覆盖。
### 3.3 模块边界

新增 `internal/catalog/repository.go`，作为清单的存储适配器：

```go
type Repository struct {
    storage bot.Storage
}

func NewRepository(storage bot.Storage) *Repository
func (r *Repository) Load(ctx context.Context) ([]Option, error)
func (r *Repository) Save(ctx context.Context, items []Option) error
```

- `Load` 读取并解码 `dinner:catalog`；键不存在时返回 `catalog.ErrNotFound`。
- `Save` 编码 `{version:1, items}` 并写入，TTL 为 `0`。
- 仓储不读取 seed 文件，不持有 `Catalog`，不缓存业务查询结果。
- 首次导入的编排放在 `plugins/dinner`，因为 seed 路径、标签校验和插件装配都属于组合根。

这样 `Catalog` 仍然是纯内存、只读的领域对象，`Repository` 单独承担持久化，不在每次推荐时增加存储 I/O。

### 3.4 首次导入流程

```text
Setup
  ├─ 解码 config：tag_groups、catalog_seed
  ├─ 校验标签词表
  ├─ repository.Load(ctx)
  │    ├─ 找到 dinner:catalog → 解码 items
  │    └─ bot.ErrNotFound     → catalog.ErrNotFound
  ├─ 若未找到：读取 seed → catalog.New → tags.validateOptions
  ├─ 校验通过后 repository.Save(ctx, items)
  └─ 构建静态 Catalog，注册命令
```

关键顺序：

1. 先校验 seed 的结构和标签，再写入存储，避免把无效清单持久化。
2. 存储已有数据时不再读取 seed；即使 seed 文件被删除，已有部署仍可启动。
3. 只有 `bot.ErrNotFound` 触发导入；其他读取错误、解码错误、版本错误都使启动失败，不回退到 seed。
4. 并发首次启动时，多个实例写入相同 seed 内容，结果幂等；不做额外的分布式锁。

### 3.5 更新与重新导入

第一阶段不提供运行时热更新：

- 修改 seed 后，已有 `dinner:catalog` 不会自动变化。
- 需要重新导入时，先停止宿主，删除 `dinner:catalog` 后重启。当前 SQLite 配置可执行：

  ```sh
  sqlite3 ./kei.db "DELETE FROM kei_storage WHERE key = 'dinner:catalog';"
  ```

  其他存储后端删除同名键。
- 删除键不会影响个人状态、投票、已吃历史；它们使用不同的键前缀。
- 后续如需管理命令，可在 `Repository.Save` 之上增加受权限控制的 `/dinner-admin`，不在本阶段实现。

## 4. 代码改动

### 4.1 新增 `internal/catalog/repository.go`（已实现）

约 100–130 行：

- `catalogKey = "dinner:catalog"`
- `catalogDocument{Version int, Items []Option}`
- `ErrNotFound`
- `Load`：`Get`、`bot.ErrNotFound` 映射、JSON 解码、版本检查。
- `Save`：结构校验、JSON 编码、`Set(..., 0)`。
- 错误统一加 `load dinner catalog:`、`save dinner catalog:` 前缀。

### 4.2 修改 `plugins/dinner/plugin.go`（已实现）

约 +25/−8 行：

- 保留现有 seed 读取与标签校验。
- 增加 `catalog.NewRepository(pc.Storage)`。
- 按 3.4 的顺序处理 `ErrNotFound`、seed 导入和 `Save`。
- `pc.Storage` 为空时返回明确错误；当前命令状态本来也依赖存储，不新增无存储运行模式。

示意：

```go
repo := catalog.NewRepository(pc.Storage)
items, err := repo.Load(ctx)
seeded := false
if errors.Is(err, catalog.ErrNotFound) {
    items, err = loadCatalogSeed(config.CatalogSeed)
    seeded = true
}
if err != nil {
    return err
}
c, err := catalog.New(items)
if err != nil {
    return err
}
if err := tags.validateOptions(c.List()); err != nil {
    return err
}
if seeded {
    if err := repo.Save(ctx, c.List()); err != nil {
        return err
    }
}
p.catalog = c
```

### 4.3 测试（已实现）

新增 `internal/catalog/repository_test.go`，约 250–350 行，覆盖：

| 场景 | 断言 |
|---|---|
| 首次 Load 未找到 | 返回 `ErrNotFound`，不写存储 |
| 首次导入 | 校验后 Save 写入 `dinner:catalog`，版本为 1 |
| 已有存储 | 直接返回存储内容，不读取 seed |
| 存储读取错误 | 保留错误链，不回退 seed |
| JSON 损坏 | 启动失败，不覆盖原值 |
| 未知版本 | 启动失败 |
| 无效清单 | Save 前校验失败，不写入 |
| 空清单 | 允许保存和读取（推荐时提示暂无候选） |
| TTL | `Set` 的 TTL 为 0 |

修改 `plugins/dinner/plugin_test.go`：

- 保留临时 seed 文件夹具。
- 增加“首次启动写入存储”“重启读取存储”“seed 修改后不覆盖存储”测试。
- 更新无存储的配置校验测试，明确返回存储未配置。

修改 `plugins/dinner/integration_test.go`：

- 首次启动后检查 `dinner:catalog` 已写入。
- 修改 seed 后重启，确认仍使用已导入清单。
- 删除 `dinner:catalog` 后重启，确认重新导入新 seed。
- 保持现有投票、推荐、已吃、历史重启恢复断言。

## 5. 验收清单

- [x] `config.yaml` 不含餐厅数组，只保留 `catalog_seed`。
- [x] 空存储首次启动后存在 `dinner:catalog`，内容来自 seed。
- [x] 第二次启动不读取 seed；修改 seed 不影响已有清单。
- [x] 删除 `dinner:catalog` 后重启，导入当前 seed。
- [x] 无效 seed、损坏存储、未知版本均使启动失败，且不覆盖原值。
- [x] 标签词表仍从 `config.yaml` 读取，seed 标签校验通过后才能写入。
- [x] `/dinner`、投票、推荐、换一个、已吃、历史行为不变。
- [x] `gofmt -l .` 无输出；`go vet ./...`、`go test ./...`、`go test -race ./...`、`go build ./...` 通过。
- [x] Mock + SQLite 集成测试通过。

## 6. 改动规模预估

| 文件 | 预计行数 |
|---|---:|
| `internal/catalog/repository.go` | 100–130 |
| `internal/catalog/repository_test.go` | 250–350 |
| `plugins/dinner/plugin.go` | +25/−8 |
| `plugins/dinner/plugin_test.go` | +80/−30 |
| `plugins/dinner/integration_test.go` | +40/−10 |
| 文档 | 100–150 |
| **合计** | **约 600–800 行，其中测试约 350–450 行** |

生产代码约 130–160 行，改动集中在清单仓储和插件装配，不触碰决策算法。

## 7. 风险与取舍

| 风险 | 处理 |
|---|---|
| seed 路径依赖工作目录 | 文档要求宿主使用绝对路径；独立启动从仓库根目录运行 |
| 首次导入失败 | 启动失败并保留错误；不静默使用内存清单，避免“看似成功但未持久化” |
| 存储已有数据但 seed 丢失 | 不读取 seed，正常启动 |
| 存储内容损坏 | 启动失败；删除键后重启可重新导入，不自动覆盖 |
| 并发首次导入 | 写入相同 seed，结果幂等；第一阶段不引入锁 |
| 清单更新不可热加载 | 明确为启动期配置；删除键重启即可，后续再加管理命令 |
| 存储键格式变化 | `version` 字段显式拒绝未知版本，不静默迁移 |

不引入清单提供器接口、后台刷新、缓存层或通用迁移框架。当前只有一个存储来源和一个静态清单，保持简单；出现第二种来源或运行时更新需求时再抽象。

## 8. 实施顺序

1. 新增 `Repository` 和单元测试，不改插件行为。
2. 插件接入 `Load`／首次导入／`Save`，更新现有测试。
3. 扩展集成测试，验证首次导入、重启和重新导入。
4. 更新 README、`docs/usage.md`、`docs/system-design.md` 和本文件的状态说明。
5. 执行完整验收命令，记录结果到 `docs/implementation-results.md`。

以上步骤已完成，验收结果见 [实施记录](implementation-results.md)。

## 9. 后续可选扩展

- 受权限保护的清单管理命令：新增、修改、停用、删除。
- `Repository.Replace` 与清单版本号，支持审计和回滚。
- 如需运行时生效，再把静态 `Catalog` 改为可替换引用；决策服务不直接依赖存储。
- 多实例部署时，如需严格一致性，再评估存储事务或锁；当前 SQLite 单实例场景不提前实现。
