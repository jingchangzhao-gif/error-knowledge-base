# 本地错误预防闭环：分批施工总计划

日期：2026-10-30  
状态：施工计划，尚未开始编码  
设计依据：`docs/superpowers/specs/2026-10-30-local-error-prevention-loop-design.zh-CN.md`

## 1. 使用方式

- 每次只提交一个批次文件；执行者只实施该批次，不提前修改后续批次文件。
- 批次必须按 `01 → 02 → 03 → 04 → 05` 顺序执行。前一批的自动化测试和交接项全部通过后，才进入下一批。
- 每批都先写失败测试，再写最小实现，最后运行该批验收命令。数据库或 Git 集成测试被跳过，不算该批验收通过。
- 不创建 Git 提交、不推送、不打 tag、不发布 Release；这些动作需要项目所有者另行明确要求。
- 当前会话内顺序实施，不使用子代理。

## 2. 不变的产品边界

首个版本只交付：

```text
resolved issue → 登记 draft → 用户显式 verify → 用户显式 activate
→ 绑定 Git HEAD → 观察真实 recurrence
```

必须保持：

- 个人开发者、本地单用户；PostgreSQL 只保存元数据、关联、验证、激活和审计。
- 产物正文只存在于用户 Git 仓库；数据库和审计均不保存正文或仓库绝对路径。
- 四种类型：`regression_test`、`deterministic_check`、`agent_instruction`、`intent`。
- 四种版本状态：`draft`、`verified`、`active`、`deprecated`。
- CLI 是验证和激活的唯一入口；MCP 只能登记草稿和查询；Web 只读。
- 不生成测试，不改项目文件，不自动执行 Git 写操作，不调用模型，不访问外网。
- 不展示 `prevented_count`，不把“没有复发”换算成虚构的预防次数。

## 3. 跨批次统一契约

所有批次使用以下名称，不得另起同义字段。

### 3.1 本地 API

`schema_version` 固定为 `v1`，操作名为：

- `prevention_add`
- `prevention_verify`
- `prevention_activate`
- `prevention_list`
- `prevention_get`
- `prevention_stats`

Go 类型统一放在 `internal/contract/prevention.go`，CUE 类型统一放在 `spec/prevention.cue`。

| Go 类型 | 关键字段与含义 |
| --- | --- |
| `PreventionAddRequest` | `issue_id`、`kind`、`title`、`artifact_path`、`content_sha256`、可选 `source`/`actor` |
| `PreventionAddResponse` | `artifact`、`version`、`created`；重复内容返回既有版本且 `created=false` |
| `PreventionVerifyRequest` | `artifact_version_id`、`repo_path`、`command []string`；仓库路径只在线上传输，不落库 |
| `PreventionVerifyResponse` | `verification`、`version`；验证失败也是合法响应，`verification.passed=false` |
| `PreventionActivateRequest` | `artifact_version_id`、`repo_path` |
| `PreventionActivateResponse` | `version`、`activated`；同版本同 commit 重试返回 `activated=false` |
| `PreventionListRequest` | 可选 `issue_id`、`project_key`、`kind`、`status`、`limit`、`cursor` |
| `PreventionListResponse` | `items []PreventionArtifactView`、可选 `next_cursor` |
| `PreventionGetRequest` | `artifact_id` |
| `PreventionGetResponse` | `artifact`、`versions`、`verifications` |
| `PreventionStatsRequest` | 可选 `project_key`、`issue_id` |
| `PreventionStatsResponse` | 状态/类型计数、验证通过率、激活观察记录和成熟保护率 |

共享读取类型：

- `PreventionArtifact`：`artifact_id`、`project_key`、`kind`、`title`、`issue_ids`、`created_at`、`updated_at`。
- `PreventionVersion`：`artifact_version_id`、`artifact_id`、`version`、`artifact_path`、`content_sha256`、`status`、可选 `commit_sha`/`activated_at`、`created_at`、可选 `latest_verification`。
- `PreventionVerification`：`verification_id`、`artifact_version_id`、脱敏 `command`、可空 `exit_code`、`passed`、`duration_ms`、有界脱敏 `evidence`、白名单 `environment`、`content_sha256`、`created_at`。
- `PreventionArtifactView`：`artifact` 和该 artifact 的 `versions`。

统计类型：

- `PreventionCount`：`name`、`count`，分别用于 `by_status` 和 `by_kind`。
- `PreventionActivationStats`：`issue_id`、`artifact_id`、`artifact_version_id`、`activated_at`、`commit_sha`、`status`、`first_seen_to_activation_ms`、`recurrence_count`、`before_count`、`after_count`、`before_days`、`after_days`、`observation_status`。
- `observation_status` 只能是 `observing` 或 `mature`。
- `PreventionStatsResponse` 还包含 `verification_runs`、`verification_passed`、`verification_pass_rate`、`mature_activations`、`mature_zero_recurrence`、可空 `mature_protection_rate`、`recurring_version_ids`。

### 3.2 数据库函数与审计

`internal/database/prevention.go` 统一提供：

- `RegisterPrevention`
- `GetPreventionVersion`
- `RecordPreventionVerification`
- `ActivatePrevention`
- `ListPreventions`
- `GetPrevention`
- `PreventionStats`（批次五）

审计 action 固定为：

- `prevention.artifact.create`
- `prevention.issue.link`
- `prevention.version.create`
- `prevention.verify`
- `prevention.activate`

### 3.3 状态与幂等解释

- 登记按 `(issue_id, kind, artifact_path)` 定位 artifact。相同路径和 SHA-256 返回最新既有版本，无论其当前状态，不追加版本、不写审计；不同 SHA-256 追加 draft。
- 一个版本至多激活一次。active 版本同 commit 重试幂等；不同 commit 或 deprecated 版本必须新建 draft，不能复活。
- verified 可以重复验证；每次都追加验证和审计。最近一次失败不回退状态，但阻止激活。
- 激活新版本时，旧 active 同事务变 deprecated，并保留其 `commit_sha` 和 `activated_at` 供历史指标使用。
- 每个版本只保存一次激活证据，因此指标中的“每次激活”精确对应一个 `(issue_id, artifact_version_id)`，无需新增激活历史表。

## 4. 批次依赖与交接

| 批次 | 文件 | 交付物 | 解锁条件 |
| --- | --- | --- | --- |
| 一 | `01-contracts-and-migration.zh-CN.md` | v1 契约、Go 校验、0009 表/约束/索引/删除函数、升级测试 | CUE、Go 契约测试、真实 PostgreSQL 迁移与升级测试通过 |
| 二 | `02-core-service-and-state-machine.zh-CN.md` | 数据访问、状态机、审计、文件哈希、显式命令执行、只读 Git、后端分发 | 服务与数据库测试覆盖并发和失败路径，真实 PostgreSQL 测试通过 |
| 三 | `03-cli-and-git-e2e.zh-CN.md` | 五个 CLI 子命令、pipe client、长验证超时、真实 CLI/Git 端到端 | 完整 add→verify→activate→复发流程实际通过；stats 在批次五交付 |
| 四 | `04-mcp-and-web-readonly.zh-CN.md` | 三个 MCP 工具、issue 详情 Preventions 区域、只读 HTTP 查询 | 工具表无 verify/activate；Web 无预防写路由 |
| 五 | `05-metrics-operations-release.zh-CN.md` | 复发指标、统计页、备份/删除/升级兼容、双语文档、全量验收 | 全部质量门禁和真实恢复/端到端测试通过 |

## 5. 文件归属

- 批次一：`spec/prevention.cue`、`spec/local-api.cue`、`spec/mcp-tools.cue`、`spec/compat.cue`、`internal/contract/prevention.go`、`internal/contract/local_api.go`、`db/migrations/0009_prevention.sql` 及对应测试。
- 批次二：`internal/database/prevention.go`、`internal/service/prevention.go`、`internal/service/api.go`、`internal/service/backend.go`、`internal/capture/run.go` 及对应测试。
- 批次三：`cmd/errkb/main.go`、`cmd/errkb/cli.go`、新建 `cmd/errkb/prevention.go`、`internal/service/client.go`、`tests/e2e/prevention_loop_test.go` 及对应测试。
- 批次四：`internal/mcp/tools.go`、`internal/webshell/server.go`、`internal/webshell/detail.go`、新建 `internal/webshell/prevention.go`、`internal/webshell/assets/app.js`、`cmd/errkb/web.go` 及对应测试。
- 批次五：`internal/database/prevention.go`、`internal/database/backup.go`、`internal/webshell/stats.go`、`internal/ui/settings.go`、统计前端、运维测试和中英文文档。

跨批文件只能追加本批约定的能力，不得回改已经验收的公共语义。若必须改变，停止当前批次并先修订总计划和设计规格。

## 6. 统一验收环境

- Windows PowerShell，仓库根目录为当前工作区。
- Go 版本遵循 `go.mod`，CUE 使用 CI 固定的 `v0.17.1`，Node/pnpm 遵循 `package.json`。
- 真实 PostgreSQL 16 测试会话必须已设置 `ERRKB_TEST_DATABASE_URL`；测试使用私有 schema 或独立数据库。
- Git 端到端测试只操作 `t.TempDir()` 创建的临时仓库，不操作用户仓库的暂存区、提交或分支。
- 所有测试命令以实际退出码为准；输出 `SKIP` 的必需集成测试必须配置环境后重跑。

## 7. 最终完成定义

只有以下条件同时成立，预防闭环才算落地：

1. 五个批次的 checkbox 全部完成，且没有以后批次伪造前批验收结果。
2. `go test ./... -count=1 -p 1 -timeout 20m`、`go vet ./...`、`cue vet ./spec/...` 和 TypeScript 既有检查通过。
3. 真实 PostgreSQL 中完成 0008→0009 升级、失败迁移回滚、备份恢复和定向删除。
4. 真实临时 Git 仓库中完成登记、通过验证、激活、脏路径拒绝和激活后复发统计。
5. MCP 只有登记/查询能力，Web 没有验证、激活、命令执行或 Git 写入入口。
6. 中英文 README、功能实现说明、项目状态、隐私文档和 `CHANGELOG.md` 的 Unreleased 小节一致，不提前宣称已经发布。
