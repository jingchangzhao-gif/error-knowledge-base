# 批次一：契约、兼容规则与 0009 迁移

前置：阅读 `00-master.zh-CN.md` 和设计规格。  
本批目标：固定 Prevention v1 的线上字段和数据库结构，不实现业务服务、CLI、MCP 或 Web。

## 1. 允许修改的文件

新增：

- `spec/prevention.cue`
- `internal/contract/prevention.go`
- `internal/contract/prevention_test.go`
- `internal/contract/testdata/prevention/add-valid.json`
- `internal/contract/testdata/prevention/register-valid.json`
- `db/migrations/0009_prevention.sql`

修改：

- `spec/local-api.cue`
- `spec/mcp-tools.cue`
- `spec/compat.cue`
- `internal/contract/local_api.go`
- `internal/database/migrate_test.go`
- `internal/database/upgrade_test.go`
- `cmd/errkb/db_test.go`（仅同步迁移数量回归断言，不实现 CLI）

不得修改已发布的 `0001`–`0008`，不得提前实现 `internal/service` 或 `cmd/errkb`。

## 2. 契约与校验

- [x] 先写 `TestPreventionContracts`，覆盖主计划列出的六个请求/响应（从 `PreventionAddRequest`/`PreventionAddResponse` 到 `PreventionStatsRequest`/`PreventionStatsResponse`）、空数组、可空字段、未知字段、错误版本和全部枚举。
- [x] 在 `spec/prevention.cue` 定义 `#PreventionSchemaVersion: "v1"`、四种 `#PreventionKind`、四种 `#PreventionStatus`、两种 `#PreventionObservationStatus`，以及主计划中的全部请求、响应和共享类型。
- [x] 在 `spec/prevention.cue` 定义 `#PreventionRegisterTool`、`#PreventionListTool`、`#PreventionGetTool`，名称分别为 `err_prevention_register`、`err_prevention_list`、`err_prevention_get`；不得定义 verify/activate 工具。
- [x] `artifact_path` 使用仓库相对斜杠路径；拒绝空串、绝对路径、盘符、反斜杠、空段、`.`、`..` 和任意 `.git` 段。`content_sha256` 为 64 位小写十六进制，`commit_sha` 为 40 位小写十六进制。
- [x] `PreventionVerifyRequest.command` 至少一个非空参数，且只是 argv，不接受 shell 字符串。`repo_path` 为必填线上字段，但所有响应和持久化类型不得包含它。
- [x] 列表 `limit` 范围为 1–100；统计比率范围为 0–1；计数、耗时和有效天数不得为负；`exit_code` 用指针表达启动失败或超时。
- [x] 在 `internal/contract/prevention.go` 实现与 CUE 一致的 Go 类型和 `Validate()`。版本校验使用独立 `PreventionSchemaVersion`，不复用 ErrorEvent 的常量。
- [x] 所有自由文本持久化字段调用现有 `checkRedacted`；请求中的 `title`、可选 `source`/`actor` 和验证证据不得绕过脱敏检查。
- [x] 在 `internal/contract/local_api.go` 的 `validOperations`、`validatePayload`、`validateResult` 添加六个操作的双向分发，继续经 `decodeStrict` 拒绝未知字段。
- [x] 在 `spec/local-api.cue` 扩展 `#Operation`、`#ToolRequest`、`#ToolResponse`，并在 `spec/mcp-tools.cue` 的 `#Tools` 追加上述三个描述符。
- [x] 在 `spec/compat.cue` 新增 `prevention` 合同条目，列出本文件的定义、字段、必填项和枚举，绑定 `internal/contract/local_api.go` 的 `DisallowUnknownFields`；同时扩展 `local_api.enums.operation`。不改 `detail_view v1`，不提升现有合同版本。

## 3. 契约测试

- [x] 合法载荷：CLI add、MCP register、verify argv、activate、list 全过滤条件、get、stats，以及空统计结果。
- [x] 非法载荷：未 resolved 的业务前置条件不属于结构校验，不在此层模拟；其余覆盖错误类型/状态、错误哈希、绝对路径、`../`、`.git`、反斜杠、空 command、负数指标和未知字段。
- [x] 验证 `DecodePipeRequest` 与 `DecodePipeResponse` 对六个操作都严格解码，响应缺少必要身份或含非法状态时失败。
- [x] 用两个合法 JSON fixture 分别代表 CLI add 与 MCP register；二者形状相同，MCP 的“调用方计算哈希”不引入额外线上字段。

契约验收命令：

```powershell
go test ./internal/contract -run 'Prevention|Pipe' -count=1 -v
cue vet ./spec/...
cue vet ./spec -d '#PreventionAddRequest' internal/contract/testdata/prevention/add-valid.json
cue vet ./spec -d '#PreventionAddRequest' internal/contract/testdata/prevention/register-valid.json
cue export ./spec -e compat --out json
```

预期：全部退出 0；非法 Go 用例以“测试通过且确实拒绝”为准，不是把非法 fixture 交给 `cue vet` 期待整套命令失败。

## 4. 0009 数据结构

- [x] 先在 `internal/database/migrate_test.go` 扩展预期表和结构断言，再创建 `0009_prevention.sql`。
- [x] 创建 `prevention_artifacts`：UUID 主键、`project_key`、`kind`、`title`、`created_at`、`updated_at`、`source`、`actor`。
- [x] 创建 `prevention_artifact_versions`：UUID 主键、artifact 外键 cascade、正整数 `version`、路径、SHA-256、状态、可空 commit/激活时间、创建和来源字段；`unique(artifact_id, version)`。
- [x] 创建 `issue_preventions`：`(issue_id, artifact_id)` 复合主键，两个外键均 cascade，并保存创建和来源字段。
- [x] 创建 `prevention_verifications`：UUID 主键、版本外键 cascade、脱敏命令、可空退出码、passed、非负 duration/evidence/environment/SHA-256、创建和来源字段。
- [x] 用 CHECK 限定四种 kind、四种 status、64/40 位小写哈希和非负时长；active 必须有 commit 与激活时间，draft/verified 不得有这两个字段。
- [x] 创建部分唯一索引 `prevention_one_active_version`：`artifact_id` 上仅 `status='active'` 唯一。
- [x] 创建触发器，拒绝更新版本的 `artifact_id`、`version`、`artifact_path`、`content_sha256` 和 `created_at`；允许服务事务修改状态及激活证据。
- [x] 创建 issue→artifact、artifact→version、版本→验证时间、激活时间和 `occurrences(issue_id, occurred_at)` 索引，保证详情和窗口统计有索引可用。
- [x] `audit_log` 不添加外键，业务级联不得触及审计。

## 5. 0009 中的定向删除兼容

本批就在同一迁移内用 `CREATE OR REPLACE FUNCTION` 更新 `errkb_delete_preview` 和 `errkb_delete_scope`，避免后续再次改动已验收迁移。

- [x] project scope 找出目标 `prevention_artifacts`，预览和执行都新增四类计数：`prevention_verifications`、`issue_preventions`、`prevention_artifact_versions`、`prevention_artifacts`。
- [x] 删除顺序为验证→issue 关联→版本→artifact→既有业务行；跨 issue 的关联随目标 artifact 删除，审计保留。
- [x] connector-only 和 before-only scope 不虚构 project artifact 删除；事件保留策略不删除 active artifact 元数据。
- [x] `delete.scope` 审计的 `counts` 加入四个新计数，预览与真实执行对同一 scope 返回一致结果。

## 6. 升级与回滚测试

- [x] `TestMigrateCreatesSchemaV1` 的业务表清单加入四张新表，并断言四表字段、CHECK、部分唯一索引、不可变触发器和无审计外键。
- [x] 保留 `schema-0007.sql` fixture；`TestUpgradeFrom0007KeepsTheData` 预期应用 `0008`、`0009`，最终账本 9 行，旧表行数不变且四张新表为空。
- [x] 升级后继续验证旧 retrieval 的 `duration_ms=NULL` 和新事件写入，防止只检查表存在。
- [x] `TestUpgradeRollsBackAFailedMigration` 将故意失败的文件改为 `0010_broken.sql`；预期真实 `0008`、`0009` 成功保留，0010 的表和账本回滚，移除 0010 后重试应用为空。
- [x] `TestUpgradeRejectsANewerLedgerVersion` 和既有配置/Markdown/connector 兼容测试保持原语义。

数据库验收命令（会话必须已有真实 `ERRKB_TEST_DATABASE_URL`）：

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 1 acceptance' }
go test ./internal/database -run 'TestMigrate|TestSchemaStructure|TestUpgrade' -count=1 -p 1 -v
```

预期：无 SKIP；0007→0009 数据保留，重复迁移应用为空，0010 故障不污染 0009。

## 7. 本批完成与交接

- [x] 运行 `gofmt -w internal/contract/prevention.go internal/contract/prevention_test.go internal/contract/local_api.go internal/database/migrate_test.go internal/database/upgrade_test.go`，再运行上述全部命令。
- [x] 运行 `go test ./internal/contract ./internal/database -count=1 -p 1` 和 `git diff --check`。
- [x] 自检：没有 `auto_fix`、`approved_by_agent`、`prevented_count`，没有仓库绝对路径持久化字段，没有 verify/activate MCP 工具。

交接给批次二：稳定的 Go/CUE 类型、六个本地 API 操作名、四张表及其约束、0009 删除函数和可复用的真实数据库测试夹具。
