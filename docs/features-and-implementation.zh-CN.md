# 功能与实现说明

[English](features-and-implementation.md)

本文分别回答两个问题：

1. Error Knowledge Base 能为用户做什么？
2. 每项能力由哪些源码、契约和数据库流程实现？

本文描述的是当前源码，而不是理想中的未来版本。**已实现**表示已有代码和测试；**已接入**表示当前某个入口可以实际调用；**可发布**还要求同一套带 tag 产物通过强制 Windows 生命周期/恢复门禁与来源门禁。交付边界见 [project-status.zh-CN.md](project-status.zh-CN.md)。

## 1. 系统形态

项目是一个本地平台，不是一个越来越大的单模型插件。宿主适配器负责观察失败；中央核心统一定义什么是错误、怎样查找以及怎样复用解法。

```text
DSH 插件      Claude Hook      Codex/MCP      CLI 包装器      日志/手工输入
    \              |               |               |               /
     +-------------+---------------+---------------+--------------+
                                      |
                                ErrorEvent v1
                                      |
                    脱敏 → 分类 → 规范化 → 指纹
                                      |
                         daemon / Named Pipe 本地 API
                                      |
                 PostgreSQL + PL/pgSQL 检索、历史与审计
                                      |
                       精确匹配 → 近似候选 → 重排
                                      |
                   CLI / MCP / 托盘 / 本地 Web 工具箱
```

最重要的边界是：适配器只翻译宿主数据，不自行实现另一套存储、匹配或可信度算法。

## 2. 各语言负责什么

| 技术                | 职责                                                                                           | 主要位置                    |
| ------------------- | ---------------------------------------------------------------------------------------------- | --------------------------- |
| Go                  | 运行时核心、CLI、daemon、Named Pipe API、采集流水线、检索编排、MCP 服务、原生 UI 模型和 Web 壳 | `cmd/errkb/`、`internal/`   |
| PL/pgSQL            | 事务化记录、精确/三元组候选查询、可信度计算、保留期和定向删除                                  | `db/migrations/`            |
| CUE                 | 事件、配置、连接器能力、本地 API 与 MCP 工具的唯一契约                                         | `spec/`                     |
| TypeScript          | 原始 DSH 插件、DSH Hook、宿主侧提示注入、适配器队列和旧 Markdown 回退                          | `src/`                      |
| HTML/CSS/JavaScript | 开发中的 Windows 工具箱浏览器呈现层                                                            | `internal/webshell/assets/` |

CUE 定义数据形状和约束，不是运行时数据库；PL/pgSQL 保证数据操作的原子性，不负责界面；Go 是中央可执行程序；TypeScript 继续承担 DSH 宿主适配。

## 3. 功能总表

| 用户功能                  | 使用入口                                                           | 主要实现                                                                                                                             | 当前状态                                                          |
| ------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------- |
| 采集失败的 CLI 命令       | `errkb run -- <command>`                                           | `cmd/errkb/run.go`、`internal/capture/run.go`                                                                                        | 已实现并通过端到端测试                                            |
| 采集宿主或工具失败        | DSH Hook、Claude Hook 配置、连接器 SDK                             | `src/plugin.ts`、`src/connector.ts`、`internal/capture/connector.go`、`connectors/`                                                  | 已实现；支持范围以实测矩阵为准                                    |
| 采集普通日志              | Tailer 组件和连接器模板                                            | `internal/capture/tail.go`、`connectors/templates/cli-log/`                                                                          | 组件已实现；通用宿主实机验收未完整完成                            |
| 入库前删除敏感信息        | 所有采集与记录路径                                                 | `spec/redact.cue`、`internal/redact/`、`src/redact.ts`                                                                               | 已实现并通过语料测试                                              |
| 对技术失败分类            | 采集流水线                                                         | `internal/core/classify.go`、`src/capture.ts`                                                                                        | 已实现，范围限于可观察技术错误                                    |
| 合并重复失败              | 按项目和指纹记录事件                                               | `internal/core/fingerprint.go`、`db/migrations/0002_record.sql`                                                                      | 已实现                                                            |
| 搜索已知错误              | CLI、MCP、原生搜索框、Web 搜索                                     | `internal/database/search.go`、`candidates.go`、`rerank.go`                                                                          | CLI/MCP 与 Web 工具箱均已实现                                     |
| 记录并版本化解法          | MCP `err_record_solution`、服务/数据库 API、Web 详情编辑器         | `internal/database/solution*.go`、`0003_solutions.sql`                                                                               | 已实现；Web 写入复用 daemon 事务与审计记录                        |
| 记录解法是否有效          | `errkb feedback`、MCP `err_feedback`                               | `internal/database/solution.go`、`errkb_trust()`                                                                                     | 已实现                                                            |
| 查看问题与统计            | CLI `show`、`list`、`stats`；Web 详情/统计/审计页                  | `cmd/errkb/cli.go`、`internal/service/edit_api.go`、`internal/audit/`、`internal/ui/`、`internal/webshell/`                          | 已实现并有自动化测试覆盖                                          |
| 为本地客户端提供服务      | `errkb daemon` + Named Pipe                                        | `internal/service/daemon.go`、`pipe.go`、`api.go`                                                                                    | 已实现                                                            |
| 让 LLM 主动查询知识库     | Unreleased 源码中 `errkb mcp` 的 12 个工具                         | `internal/mcp/`                                                                                                                      | 已实现；MCP 不是被动采集器                                        |
| 登记、验证并观察预防产物  | 六个 `errkb prevention` 命令、三个读取/登记 MCP 工具、Web 只读统计 | `internal/service/prevention.go`、`internal/database/prevention.go`、`internal/mcp/prevention.go`、`internal/webshell/prevention.go` | Unreleased 源码已实现并通过本地测试；未包含在 v0.1.3              |
| 诊断安装和连接器          | `errkb doctor`                                                     | `cmd/errkb/doctor.go`、`internal/service/doctor.go`                                                                                  | 已实现                                                            |
| 应用并检查数据库迁移      | `errkb db migrate`、`errkb db status`                              | `cmd/errkb/db.go`、`internal/database/migrate.go`                                                                                    | 当前源码已实现                                                    |
| 打开本地工具箱窗口        | `errkb ui`、`errkb ui --web`                                       | `internal/ui/`、`internal/webshell/`、`cmd/errkb/web.go`                                                                             | 内嵌 Web 壳已随 v0.1.1 发布（`errkb ui --web`）                   |
| 安装和管理本地 PostgreSQL | PostgreSQL 管理器与便携脚本                                        | `internal/service/pg_manage.go`、`packaging/windows/postgres.ps1`                                                                    | 已实现；`errkb pg install` 创建实例，`autostart.ps1` 在登录时恢复 |
| 安全保留与删除数据        | 保留期/定向删除函数和设置模型                                      | `0006_retention.sql`、`internal/ui/settings.go`                                                                                      | 已实现，包含 Web 预览/确认；自动定时执行不属于 v1                 |

## 4. 错误采集

### 4.1 通用 CLI 包装器

`errkb run -- <command>` 启动子进程，转发 stdin，持续显示 stdout/stderr，并返回子进程原始退出码。成功命令不记录；非瞬时失败会转换为 `ErrorEvent v1`，先脱敏，再发送给 daemon。完成记录后会立刻检索同一事件，以便打印已有解法。

```text
cmd/errkb/run.go
  → internal/capture/run.go
  → 脱敏 + 分类 + 标题 + 指纹
  → service.Client.Capture / Named Pipe
  → service.SQLBackend.Capture
  → database.RecordEvent + database.Retrieve
```

同一命令重复失败时，会在同一个 issue 上追加 occurrence，而不是创建互不相关的问题。

### 4.2 宿主适配器

DSH 插件监听宿主事件，并且只转换宿主真实提供的字段。连接器协议提供采集、查询、通知和反馈操作。能力文件明确标记可用能力和降级项，不会猜测宿主没有提供的数据。

- DSH 实现：`src/plugin.ts`、`src/connector.ts`。
- Go 连接器契约与探针：`internal/capture/connector.go`。
- 宿主能力记录：`connectors/*/capability.json`。
- 实测行为：[connector-matrix.zh-CN.md](connector-matrix.zh-CN.md)。

Codex 和 Claude 还可以暴露 MCP 工具。MCP 必须由模型或用户主动调用；只安装 MCP 不会自动监视错误。

### 4.3 离线队列

daemon 不可用时，连接器可以暂存观测并幂等重放。Go 队列只保存已经脱敏的事件，并限制数量、字节数和保存时间（`internal/service/queue.go`）。TypeScript DSH 适配器另有本地队列（`src/state.ts`）；它暂存原始观测的隐私限制见 [privacy.zh-CN.md](privacy.zh-CN.md)。

## 5. 错误处理

### 5.1 分类

分类器根据可观察证据，把错误归入 `tool`、`command-exit`、`llm` 或 `agent`，同时推导严重度和是否属于瞬时失败。瞬时失败可以先计数，而不立即成为长期 issue。

该逻辑位于 `internal/core/classify.go` 和 `src/capture.ts`，是确定性规则，不会调用 LLM 判断。

### 5.2 脱敏

`spec/redact.cue` 是唯一规则源。生成出的 Go 和 TypeScript 规则会在数据库写入之前替换密钥、凭据、邮箱、请求 ID、用户目录和其他敏感形态；契约校验还会拒绝看起来仍然包含敏感信息的载荷。Go、TypeScript 与 CI 共用同一套测试语料。

### 5.3 规范化与指纹

规范化会移除 ANSI 转义、GUID、时间戳、PID、计数器、端口和绝对路径等不稳定值；稳定文本转为小写后计算：

```text
sha256(category + "\0" + normalized_message)[:12]
```

对应实现为 `internal/core/normalize.go`、`fingerprint.go` 和 TypeScript 的 `src/signature.ts`。指纹用于确定性身份，不代表两条近似文本一定是同一个错误。

## 6. 存储模型

PostgreSQL 是目标系统的唯一事实来源：

| 实体                                                   | 含义                                                                       |
| ------------------------------------------------------ | -------------------------------------------------------------------------- |
| `connectors`、`sessions`                               | 错误来自哪个宿主和会话                                                     |
| `events`                                               | 不可变、已脱敏的失败观测                                                   |
| `issues`                                               | 项目 + 指纹对应的可复用问题身份                                            |
| `occurrences`                                          | 问题每次重复出现的时间和环境                                               |
| `solutions`、`solution_versions`                       | 解法生命周期和追加式正文历史                                               |
| `outcomes`                                             | 某个解法版本在某种环境中是否有效                                           |
| `retrievals`                                           | 查了什么、返回什么、为何匹配、耗时多久                                     |
| `notices`                                              | 实际展示过的已知解法通知                                                   |
| `audit_log`                                            | 管理与纠错操作                                                             |
| `prevention_artifacts`、`prevention_artifact_versions` | Unreleased 预防身份、仓库相对路径、不可变内容哈希、生命周期与 Git 激活证据 |
| `issue_preventions`、`prevention_verifications`        | 同项目 issue 关联与追加式脱敏命令结果                                      |
| `schema_migrations`                                    | 已应用迁移及校验和                                                         |

`internal/database/migrate.go` 在 advisory lock 下逐个事务应用 SQL 迁移。校验和账本阻止已经执行过的迁移被悄悄改写。

`src/store.ts` 中原始 DSH Markdown 仓库仍用于兼容和回退，但不是工具箱的目标事实来源。导入/导出兼容逻辑在 `internal/database/markdown.go`。

## 7. 检索与解法复用

检索刻意分为四个阶段：

1. **精确检索：** issue ID、指纹、错误码、规范化消息、命令或工具名（`0004_exact_search.sql`、`internal/database/search.go`）。
2. **候选生成：** PostgreSQL `pg_trgm` + GIN 索引产生有限候选（`0005_trigram.sql`、`internal/database/candidates.go`）。
3. **重排：** Go 使用 Jaccard 比较 token 集合，排除结构化字段矛盾，计算环境差异，最后最多返回 3 条（`internal/core/rerank.go`、`internal/database/rerank.go`）。
4. **呈现：** 每条结果都包含匹配原因与命中字段；非精确结果带 `approximate: true`，必须确认。

近似相似度可以推荐解法，但不能静默合并两个 issue，也不能自动执行修复。性能证据见 [benchmarks.zh-CN.md](benchmarks.zh-CN.md)。

## 8. 解法生命周期与可信度

`solutions` 保存状态，`solution_versions` 追加保存每个正文版本。MCP `err_record_solution` 或服务 API 可以为已知问题记录并验证新解法；纠错操作可以把 issue 设为 `open`、`resolved`、`wontfix` 或 `misjudged`。

用户和宿主针对具体解法版本报告 `success`、`failure` 或 `unknown`。可信度由结果计算，不能手工填写：

```text
trust = (successes + 2) / (successes + failures + 4)
```

`unknown` 不改变分数。结果带环境，因此界面可以在操作系统、shell、连接器或运行时不同的时候显示适用性警告。

### 8.1 预防闭环（Unreleased）

四种预防类型为 `regression_test`、`deterministic_check`、`agent_instruction`
和 `intent`。登记要求已解决 issue，并创建或关联不可变的内容哈希版本。
激活前要求最新验证针对相同哈希通过，还要求产物干净且已跟踪于 Git
`HEAD`。替代版本会弃用旧 active 版本但保留历史；复发不会自动改写状态。

```text
cmd/errkb/prevention.go
  → service.HashPreventionArtifact（add / verify / activate）
  → service.Client / Named Pipe / API.Handle
  → service.SQLBackend.Prevention*
  → database.RegisterPrevention / RecordPreventionVerification /
    ActivatePrevention / ListPreventions / GetPrevention / PreventionStats
  → 0009 迁移的表、约束、索引和 audit_log
```

验证使用 `capture.Run`，stdin 为空、截止时间为 120 秒、输出有界，并在
运行前后检查产物哈希。只有 CLI 提供 verify/activate。MCP
`err_prevention_register/list/get` 复用同一后端，但 register 接收元数据和
调用方哈希，不读取文件。Web 只通过 `client.PreventionStats` 暴露
`GET /api/preventions/stats`。指标由 `database.PreventionStats` 的 SQL 聚合
基于真实 occurrence 计算。完整工作流见 [prevention.zh-CN.md](prevention.zh-CN.md)。

## 9. 对外入口

### 9.1 CLI

CLI 是同一套契约上的薄客户端，包括 `search`、`show`、`record`、`list`、`feedback`、`stats`、`prevention`、`run`、`db`、`daemon`、`doctor`、`mcp`、`ui` 和 `version`。人类可读输出与 `--json` 共用同一份已校验响应。

### 9.2 Daemon 与本地 API

`errkb daemon` 持有 PostgreSQL 连接池，通过 `\\.\pipe\errkb` 提供带帧 JSON 请求。Named Pipe ACL 只允许当前用户。daemon 保证每个 Windows 会话只有一个实例，拒绝未迁移数据库，写机器可读健康文件，轮转脱敏日志，并在退出时等待正在执行的请求。

### 9.3 MCP

Unreleased 源码中的 `errkb mcp` 通过 stdio 提供 12 个工具；v0.1.3 提供前 9 个：

| 工具                      | 作用                                                 |
| ------------------------- | ---------------------------------------------------- |
| `err_lookup`              | 按 issue、错误码、指纹或规范化文本进行偏精确查询     |
| `err_search`              | 自由文本检索，明确标记近似候选                       |
| `err_get_issue`           | 读取单个问题及其出现记录、解法和版本                 |
| `err_record`              | 显式提交一条已经脱敏的事件                           |
| `err_feedback`            | 记录解法是否有效                                     |
| `err_list`                | 列出问题或解法                                       |
| `err_stats`               | 读取总量、延迟和节省估算                             |
| `err_record_solution`     | 为已知问题新增并验证解法                             |
| `err_set_status`          | 纠正问题状态                                         |
| `err_prevention_register` | 登记预防元数据和调用方提供的内容哈希，不读取产物文件 |
| `err_prevention_list`     | 列出预防产物及其当前生命周期状态                     |
| `err_prevention_get`      | 读取一个预防产物及其版本、关联和验证历史             |

MCP 层调用 daemon 后端并重新校验返回值，不直接连接 PostgreSQL，也不会被动监视宿主。

### 9.4 原生与 Web UI

`internal/ui/` 包含经过测试的托盘、搜索框、问题详情、解法编辑、统计和设置状态模型；`internal/webshell/` 通过内嵌浏览器界面呈现这些能力。

本地 `errkb ui --web` 会嵌入静态资源，只绑定 `127.0.0.1`，每次启动生成随机令牌，并设置严格安全响应头。搜索、问题详情与编辑、统计、审计、设置、保留策略及删除预览/确认均已接到现有 daemon 服务。Unreleased 源码还展示只读的预防统计；预防登记、验证、激活和 issue 关联仍由 CLI/MCP 操作。详见 [toolbox-ui.zh-CN.md](toolbox-ui.zh-CN.md)。

## 10. 运维、隐私与恢复

- `errkb doctor` 对配置、数据库/schema、隐私模式、队列健康和连接器能力执行只读检查。
- `errkb db migrate/status` 可以在不启动 daemon 的情况下应用迁移或比对账本。Unreleased 源码新增 `0009_prevention.sql`；升级、回滚和未来账本拒绝均使用真实 PostgreSQL 测试。
- PostgreSQL 生命周期代码可以发现二进制、创建或接管本地实例、生成凭据、启停、备份、升级和卸载（`internal/service/pg_manage.go`）。便携备份/恢复覆盖全部四张预防表及其不可变版本、issue 关联和验证结果。
- 保留策略分别控制样本、规范化事件和审计记录；定向删除支持预览、确认和审计（`0006_retention.sql`），包括预防产物与跨 issue 关联；保留任务会保留 active 产物及预防指标使用的剩余 occurrence 证据。
- 崩溃测试会真实终止写入与迁移进程，再验证已提交数据完整性。

Windows 冒烟任务会构建发布二进制并用真实 PostgreSQL 跑全套测试；`release.ps1` 于 2026-10-25 生成并核对便携 `v0.1.3` 发布包，安装器路线已移除。

## 11. 明确不提供的功能

版本一不会：

- 读取或保存模型的私有思维链；
- 可靠判断模型是否误解需求或产生幻觉；
- 通过截图或 OCR 监视所有桌面程序；
- 自动执行检索到的修复；
- 自动生成回归测试、编辑预防产物文件或写入 Git；
- 调用模型或外部服务来做出预防决策；
- 上传知识库或提供团队/云同步；
- 使用 embedding 或向量检索。

这些边界让首个版本专注于可观察、可复现的技术错误。

## 12. 怎样安全扩展功能

1. 新增或复用 CUE 契约，不创建只属于某个界面的私有字段。
2. 增加能力探针，对宿主不支持的字段明确标记。
3. 把输入转换为 `ErrorEvent v1`，并在持久化或排队前脱敏。
4. 复用 daemon/backend 操作，不在适配器中复制检索算法。
5. 返回匹配原因、近似标记、环境适用性和可信度。
6. 增加单元测试、契约/语料测试和真实宿主验收记录。
7. 同步更新本文中英文版本、连接器矩阵与项目状态。

这个顺序可以保持唯一的错误身份来源，避免每接入一个模型就产生一套独立产品。
