# 本地错误预防闭环 v1 设计规格

日期：2026-10-30  
状态：等待项目所有者确认  
目标代码基线：`error-knowledge-base` v0.1.3 之后的 `main`

## 1. 结论

下一阶段不把项目改造成自动修复 Agent，也不引入团队云服务。首个施工版本只交付一条可验证的本地垂直闭环：

```text
已解决 issue
  → 登记一个仓库内预防产物
  → 用户显式运行验证命令
  → 用户显式激活已验证版本
  → 关联 Git commit
  → 观察激活后的真实复发
```

产品从“同错快速修复”扩展为“同错快速修复 + 同类错误预防”，但现有采集、检索、解法、可信度和隐私边界保持不变。

## 2. 目标与非目标

### 2.1 必须实现的目标

1. 用户可以把一个已解决 issue 关联到回归测试、确定性检查、Agent 指令或 `intent.md`。
2. 预防产物正文只存在于用户指定的 Git 仓库；PostgreSQL 保存元数据、关联、验证和审计，不维护第二份正文。
3. 每次验证绑定一个不可变产物版本及其 SHA-256；验证后内容变化则不能直接激活。
4. 只有用户通过 CLI 显式执行 `verify` 和 `activate`，才能改变验证和激活状态。
5. Agent 可以通过 MCP 登记草稿和读取状态，但不能通过 MCP 验证、激活或执行命令。
6. 工具箱可以回答：哪些错误已有预防、预防是否验证、激活后是否仍复发。
7. 整个流程不调用模型、不访问外网、不自动写项目文件、不执行 Git 写操作。

### 2.2 本规格明确不做

- 自动生成回归测试、Hook、规则、技能或修复代码。
- 自动修改 `CLAUDE.md`、`AGENTS.md`、测试或项目配置。
- 自动 `git add`、`git commit`、创建 PR 或部署。
- 生产监控、异常控制带、自动诊断和自主 PR。
- 团队权限、云同步、多用户和跨机器知识共享。
- 向量检索、embedding 或把模型放入 Go 核心。
- 把“激活后没有出现”解释为确定预防了多少次错误。

这些能力进入后续独立规格，不能阻塞本垂直切片。

## 3. 核心概念

### 3.1 PreventionArtifact

一个持续存在的预防意图，例如“防止 Windows 锁竞争再次导致并发写入失败”。它属于一个 `project_key`，可以关联多个 issue。

产物类型固定为：

- `regression_test`：代码回归测试或 Agent eval。
- `deterministic_check`：doctor、lint、Hook 或 CI 检查。
- `agent_instruction`：供 Agent 阅读的简短规则；它只是建议性控制。
- `intent`：需要后续设计或较大改动的 `intent.md`。

### 3.2 PreventionArtifactVersion

产物的一次不可变内容版本，保存仓库相对路径和内容 SHA-256。正文不进入数据库。每次内容变化必须新增版本，不能覆盖旧版本。

版本状态：

```text
draft → verified → active → deprecated
  └──────────────────────→ deprecated
```

规则：

- 新建版本从 `draft` 开始。
- 只有同一版本、同一 SHA-256 的成功验证才能使其进入 `verified`。
- 一个 artifact 同时最多一个 `active` 版本。
- 激活新版本时，旧活动版本在同一事务内变为 `deprecated`。
- 新增草稿版本不影响当前活动版本。
- 不允许从 `deprecated` 恢复；需要时创建新版本。

### 3.3 验证与激活的区别

- **验证**是机械证据：用户显式给出的命令在该版本上退出码为 0。
- **激活**是人的决定：用户确认该版本进入项目当前 HEAD，并允许它参与后续复发统计。
- 验证成功不代表自动激活；Agent 不能替代用户完成激活。

## 4. 用户流程

### 4.1 登记

前置条件：目标 issue 存在且状态为 `resolved`。

用户在仓库内准备好产物文件后运行：

```powershell
errkb prevention add `
  --issue <issue-id> `
  --kind regression_test `
  --title "Windows concurrent lock regression" `
  --repo <repository-path> `
  --path tests/windows_lock_regression_test.go
```

系统：

1. 校验 issue、类型和标题。
2. 校验 `path` 为仓库相对路径，拒绝绝对路径、`..`、`.git` 和空路径。
3. 读取不超过 1 MiB 的文件并计算 SHA-256；不保存正文和仓库绝对路径。
4. 以 `(issue_id, kind, artifact_path)` 定位已有 artifact；不存在则创建并建立 issue 关联。
5. 同一 artifact 上，相同 `artifact_path + content_sha256` 的登记幂等返回现有 draft/verified 版本，不重复写审计；不同哈希追加下一个 draft 版本。
6. 新建 artifact、关联或版本时写入审计记录；纯幂等读取不新增审计。

### 4.2 验证

```powershell
errkb prevention verify <version-id> --repo <repository-path> -- go test ./tests -run WindowsLock
```

系统：

1. 检查版本为 draft 或 verified；deprecated/active 版本不能重复验证。
2. 重新计算产物哈希，必须等于登记值。
3. 直接启动用户命令，不隐式经过 shell；默认 120 秒超时。
4. 保留命令退出码、耗时、环境摘要和有界脱敏证据，不保存完整原始输出。
5. 验证前后产物哈希必须一致；验证命令改变产物则本次失败。
6. 退出码 0 记录 passed，版本进入 verified；非 0 或超时记录失败，版本保持原状态。
7. 每次验证只追加一条记录，不覆盖历史；已经 verified 的版本允许重新验证，但最近一次验证失败会阻止激活。

### 4.3 激活

```powershell
errkb prevention activate <version-id> --repo <repository-path>
```

系统：

1. 要求版本为 verified，且最近一次验证为 passed、对应当前内容哈希。
2. 再次计算文件哈希；不一致则拒绝。
3. 只读调用 Git，要求路径在 HEAD 中、工作区该路径无未提交变化，并取得当前 40 位 HEAD SHA。
4. 不执行 checkout、add、commit、push 或任何其他 Git 写操作。
5. 在一个数据库事务内废弃旧活动版本、激活新版本、记录 commit 和激活时间、写审计。
6. 重复激活同一版本且 commit 未变化时幂等返回现有状态，不重复写审计。

### 4.4 观察

用户可在 issue 详情和统计页看到：

- 关联产物及其类型、版本和状态。
- 最近验证结果与激活 commit。
- 激活前后 30 天窗口的出现次数。
- 激活后再次出现的次数；出现新 occurrence 不自动停用产物，也不自动修改 issue 状态。

## 5. 架构与责任边界

```text
CLI / MCP / Web toolbox
          │
          ▼
versioned prevention contracts
          │
          ▼
local daemon / Named Pipe
          │
          ▼
prevention application service
          │
          ├── PostgreSQL：元数据、关联、验证、激活、审计
          └── local filesystem + read-only Git：哈希与提交证据
```

责任划分：

- Go 核心负责状态机、校验、哈希、显式验证执行和指标。
- PostgreSQL 负责关联完整性、单活动版本约束、状态转移和审计事务。
- CUE 定义新契约，并纳入现有兼容检查。
- CLI 是唯一可以运行验证命令和执行激活的入口。
- MCP 只提供草稿登记与只读查询。
- Web 工具箱只展示详情、指标和对应 CLI 提示，不开放命令执行。
- 现有 ErrorEvent v1 不增加预防字段；预防能力使用独立 v1 契约，避免破坏采集协议。

## 6. 数据库设计

新增迁移 `0009_prevention.sql`，不得修改已经发布的 0001–0008。

### 6.1 `prevention_artifacts`

字段：

- `id uuid primary key`
- `project_key text not null`
- `kind text not null`，限定四种产物类型
- `title text not null`
- `created_at`、`updated_at`
- `source`、`actor`

### 6.2 `prevention_artifact_versions`

字段：

- `id uuid primary key`
- `artifact_id uuid not null references prevention_artifacts on delete cascade`
- `version integer not null check version >= 1`
- `artifact_path text not null`
- `content_sha256 text not null`，64 位小写十六进制
- `status text not null`，限定 draft/verified/active/deprecated
- `commit_sha text null`，40 位小写十六进制
- `activated_at timestamptz null`
- `created_at`、`source`、`actor`
- `unique (artifact_id, version)`

使用部分唯一索引保证同一 artifact 最多一个 active 版本。版本正文相关字段不可更新；状态、commit 和激活时间只能经服务事务改变。

### 6.3 `issue_preventions`

字段：

- `issue_id uuid not null references issues on delete cascade`
- `artifact_id uuid not null references prevention_artifacts on delete cascade`
- `created_at`、`source`、`actor`
- `primary key (issue_id, artifact_id)`

### 6.4 `prevention_verifications`

字段：

- `id uuid primary key`
- `artifact_version_id uuid not null references prevention_artifact_versions on delete cascade`
- `command text not null`，写入前脱敏
- `exit_code integer null`
- `passed boolean not null`
- `duration_ms integer not null`
- `evidence text not null`，有界且脱敏
- `environment jsonb not null`
- `content_sha256 text not null`
- `created_at`、`source`、`actor`

### 6.5 运维兼容

- 定向删除预览和执行必须计入新表。
- 保留策略不得静默删除仍处于 active 状态的产物元数据。
- 备份/恢复测试必须覆盖新表、版本状态、关联和验证记录。
- 审计继续只写 `audit_log`，业务行删除不能级联删除审计。
- 为 issue 关联、artifact 版本、激活时间和验证记录建立必要索引；激活后统计不得全表扫描 occurrences。

## 7. 契约与接口

新增 `spec/prevention.cue`，契约版本为 v1，并登记到 `spec/compat.cue`。

所有请求和响应：

- 携带 `schema_version`。
- 默认拒绝未知字段。
- 所有自由文本在持久化前经过统一脱敏。
- 路径只使用仓库相对斜杠路径，数据库不保存仓库绝对路径。
- 不引入 `auto_fix`、`approved_by_agent`、`prevented_count` 或可直接写状态的信任字段。

本地 API 增加：

- `prevention_add`
- `prevention_verify`
- `prevention_activate`
- `prevention_list`
- `prevention_get`
- `prevention_stats`

verify 和 activate 只接入本地 CLI 工作流，不接入 MCP 或 Web。人工批准是产品流程约束，不是对不同本地进程身份的密码学认证；防止 Agent 自动激活依赖 MCP 工具面不暴露这些操作。

## 8. CLI、MCP 与 Web

### 8.1 CLI

新增命令：

```text
errkb prevention add
errkb prevention verify
errkb prevention activate
errkb prevention list
errkb prevention show
errkb prevention stats
```

约定：

- 所有写操作输出 artifact/version id、当前状态和下一步命令。
- `--json` 返回经过契约校验的结构，不另行拼造字段。
- 验证失败返回非零退出码，但保留失败验证记录。
- 激活失败不改变旧活动版本。
- verify 的进程捕获复用现有有界输出、超时、环境白名单和脱敏机制。

### 8.2 MCP

只新增：

- `err_prevention_register`：创建 draft；必须提供 issue、类型、标题、相对路径和调用方计算的 SHA-256。
- `err_prevention_list`：按 issue、project、kind、status 查询。
- `err_prevention_get`：读取版本、验证和激活历史。

MCP 明确不提供 verify、activate、命令执行、文件写入或 Git 写入。Agent 可以建议下一步，但不能宣称已验证或已激活。

### 8.3 Web 工具箱

首个版本只做只读呈现：

- issue 详情增加 Preventions 区域。
- 统计页增加 Prevention 区域。
- 每个草稿显示对应的 verify CLI 提示，每个已验证版本显示 activate CLI 提示。
- 不提供 Web 命令执行框，不通过浏览器接收仓库密码或凭据。

## 9. 指标定义

统计必须区分机械事实和解释，禁止展示无依据的“已预防 N 次”。

必须提供：

1. 各状态、各类型产物数量。
2. 验证通过率：`passed verification runs / all verification runs`。
3. 首次出现到激活的耗时：按 issue 的 `first_seen` 到首次关联产物 `activated_at` 计算。
4. 激活后复发数：关联 issue 在 `activated_at` 之后产生的 occurrence 数。
5. 激活前后 30 天窗口：按 `(issue_id, artifact_version_id)` 的每次激活分别计算；前窗为 `[T-30d, T)`，后窗为 `(T, T+30d]`。
6. 前窗有效天数为 `min(30, T-first_seen)` 的完整天数，后窗有效天数为 `min(30, now-T)` 的完整天数；窗口内 occurrence 数和有效天数同时展示。
7. 成熟保护率：后窗已满 30 天的激活记录中，后窗零复发的比例。
8. 仍复发的活动产物列表，供用户决定新增版本、废弃或创建新 intent。

后窗不足 30 天时显示“观察中”，该记录不进入成熟保护率分子或分母。前窗或后窗不足 30 天时不得直接声称下降比例；同一 issue 多次激活按不同激活记录计算，不合并成无法解释的总数。

## 10. 安全、隐私与故障处理

- verify 只能由用户显式运行；不存在后台定时验证或 MCP 验证。
- 不隐式使用 shell，不自动执行产物正文中的命令。
- 默认 120 秒超时；进程取消和子进程回收复用现有执行层。
- 输出按现有策略有界保存并脱敏；仓库绝对路径不进入数据库。
- 文件上限 1 MiB；符号链接、设备文件和越出仓库的路径拒绝。
- 文件哈希变化、Git 工作区不干净、HEAD 不包含路径、版本状态不合法均明确失败。
- 数据库不可用时不能先改变文件或 Git 再补登记。
- 激活事务失败时保留旧活动版本。
- 宿主和 Agent 不可用不影响 CLI 的本地预防闭环。
- 预防失败不中断原有错误采集和检索。

## 11. 测试与验收

### 11.1 自动化测试

- CUE 合法/非法载荷、未知字段、路径和哈希校验。
- schema 0008→0009 升级、回滚和旧数据保留。
- 版本状态机、单活动版本、并发激活和旧版本废弃。
- 重复登记、重复验证和审计幂等。
- 内容变化后激活拒绝。
- 验证超时、非零退出、输出截断和脱敏。
- Git 未跟踪路径、未提交变化和非 Git 仓库拒绝。
- MCP 工具表不包含 verify/activate，写操作只能创建 draft。
- 定向删除、备份恢复和审计覆盖新实体。
- 指标边界：不足 30 天、窗口长度不同、重复 occurrence 和多 issue 关联。
- 现有 Go、TypeScript、CUE、兼容和隐私测试全部保持通过。

### 11.2 端到端验收

在临时真实 Git 仓库和真实 PostgreSQL 中：

1. 记录一个失败并把 issue 设为 resolved。
2. 新建一个回归测试文件并提交。
3. 通过 CLI 登记 draft。
4. 运行真实验证命令并使其通过。
5. 激活版本，确认数据库记录当前 HEAD。
6. 修改产物但不提交，确认再次激活被拒绝。
7. 再记录一次相同错误，确认统计展示一次激活后复发。
8. 通过 MCP 尝试寻找 verify/activate，确认工具不存在。
9. 全程无模型调用、无外联、无自动 Git 写操作。

完成标准不是“表和命令存在”，而是以上用户流程全部实际运行通过，并同步更新中英文 README、功能实现说明、项目状态、隐私和变更日志。

## 12. 建议施工拆分

本规格按以下顺序形成实施计划：

1. 契约、兼容规则与 0009 迁移。
2. Go 服务、状态机、审计和数据库测试。
3. CLI 登记、验证、激活和真实 Git 端到端流程。
4. MCP 草稿/只读接口和 Web 只读呈现。
5. 指标、运维兼容、双语文档和发布验收。

每一步都必须能独立验证，不并行铺开自动生成或异常监控。

## 13. 后续独立版本

本垂直切片验收后，再分别评审：

1. Agent 根据 issue 生成回归测试或检查草稿，但仍由用户验证和激活。
2. 把重复 Agent 错误转成简短指令候选，并用 eval 证明有效。
3. 对必须强制执行的规则生成确定性 Hook/CI 导出。
4. 对复发率和验证失败率增加确定性控制带，只生成待分诊 intent。
5. 最后才考虑受限的自动 PR，且永远需要人工审核。

这样可以保持项目始终是本地错误知识库，而不是失控扩张成完整 SDLC 平台。
