# 批次五：复发指标、运维兼容、双语文档和发布验收

前置：批次四全部验收通过。  
本批目标：交付 `prevention stats`、统计页、完整数据生命周期和最终验收；只更新 Unreleased，不打 tag、不发布。

## 1. 允许修改的文件

核心与接口：

- `internal/database/prevention.go`
- `internal/database/prevention_test.go`
- `internal/service/prevention.go`
- `internal/service/prevention_test.go`
- `internal/service/api.go`
- `internal/service/backend.go`
- `internal/service/client.go`
- `internal/service/client_test.go`
- `cmd/errkb/prevention.go`
- `cmd/errkb/prevention_test.go`
- `tests/e2e/prevention_loop_test.go`

Web 与运维：

- `internal/webshell/server.go`
- `internal/webshell/stats.go`
- `internal/webshell/stats_test.go`
- `internal/webshell/prevention.go`
- `internal/webshell/prevention_test.go`
- `internal/webshell/assets/app.js`
- `cmd/errkb/web.go`
- `internal/database/backup.go`
- `internal/database/backup_test.go`
- `internal/ui/settings.go`
- `internal/ui/settings_test.go`
- `spec/compat.cue`（仅升级保证和矩阵）
- `db/migrations/README.md`

双语文档：

- `README.md`、`README.zh-CN.md`
- `docs/features-and-implementation.md`、`docs/features-and-implementation.zh-CN.md`
- `docs/project-status.md`、`docs/project-status.zh-CN.md`
- `docs/privacy.md`、`docs/privacy.zh-CN.md`
- 新增 `docs/prevention.md`、`docs/prevention.zh-CN.md`
- `CHANGELOG.md`

不得修改 0001–0009 迁移内容，不得提升版本号或宣称已经发布。

## 2. 指标计算

- [ ] 先写固定时间测试，再实现 `database.PreventionStatsAt(ctx, conn, filter, now)`；另提供 `PreventionStats` 使用当前 UTC 时间，线上请求不接受调用方伪造的 `as_of`。
- [ ] `by_kind` 按 distinct artifact 计数，`by_status` 按 distinct artifact version 计数；多 issue 关联不能重复放大总数。
- [ ] 验证通过率固定为 `passed runs / all runs`，同时返回分子和分母；0 次运行时比率为 0，并显示 runs=0。
- [ ] 每条有 `activated_at` 的版本，对每个关联 issue 生成一条 `PreventionActivationStats`；`first_seen_to_activation_ms=max(0,T-issue.first_seen)`。
- [ ] 前窗为 `[T-30d,T)`，后窗为 `(T,T+30d]`；窗口边界上的 occurrence 必须分别测试，T 本身既不计入前窗也不计入后窗。
- [ ] `before_days=clamp(floor((T-first_seen)/24h),0,30)`，`after_days=clamp(floor((now-T)/24h),0,30)`；不足一个完整 24 小时计 0。
- [ ] `before_count`/`after_count` 只统计各自 30 天窗；`recurrence_count` 统计 T 之后全部 occurrence。三个数字不得混用。
- [ ] `after_days<30` 为 `observing`，不进入成熟保护率；满 30 天为 `mature`。成熟保护率为成熟记录中 `after_count=0` 的比例，分母为 0 时字段为 null。
- [ ] 前窗或后窗不足 30 天只展示计数与有效天数，不计算下降百分比；任何响应和 UI 都不得出现 `prevented_count`。
- [ ] `recurring_version_ids` 只包含当前 active 且 `recurrence_count>0` 的版本；新 occurrence 不自动 deprecated，也不改 issue 状态。
- [ ] project/issue 过滤应用于所有聚类、验证和激活记录；查询使用批次一的 occurrence/activation 索引，不用 Go 全表拉取后计算。

固定时间用例：

- [ ] first_seen 不足 30 天、激活后不足 30 天、恰好 30 天、超过 30 天。
- [ ] T-30d、T、T+30d 三个边界 occurrence，以及同一 issue 重复 occurrence。
- [ ] 一个 artifact 关联两个 issue 时分别形成两条激活观察，但 artifact 总数只计一次。
- [ ] 同一 issue 的多个版本分别统计；deprecated 版本保留历史，active 版本单独进入仍复发列表。
- [ ] 成熟零复发、成熟有复发、观察中混合时，分子分母和 nullable rate 正确。

数据库验收：

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 5 acceptance' }
go test ./internal/database -run 'PreventionStats|Prevention' -count=1 -p 1 -v
```

## 3. stats API、CLI 与 Web

- [ ] 扩展 `service.Backend`、`API.Handle`、`SQLBackend` 和 `service.Client`，接入批次一已定义的 `prevention_stats`；保持其他操作不变。
- [ ] 新增 `errkb prevention stats [--project <key>] [--issue <id>]`，输出状态/类型数量、验证分子分母、激活观察记录、成熟保护率和仍复发版本；`--json` 原样返回契约。
- [ ] Web 新增 `PreventionStatsFunc`、`EnablePreventionStats` 和 `GET /api/preventions/stats`，经 `client.PreventionStats` 调用守护进程；无 prevention POST 路由。
- [ ] 统计页新增 Prevention 区域：计数、验证通过率、每次激活的有效天数/窗口计数/observing 或 mature、成熟保护率和仍复发列表。
- [ ] 对 null 成熟保护率显示“暂无成熟记录”；观察中显示“观察中”，不显示 0% 或 100% 来冒充结论。
- [ ] 所有文本转义，CLI 提示保持只读；既有核心指标、审计和设置页行为不变。
- [ ] 扩展真实 CLI E2E：激活后再次记录同错，`prevention stats --issue` 显示 recurrence_count=1、相应 observing 记录，且成熟保护率不包含它。

命令：

```powershell
go test ./internal/service ./cmd/errkb ./internal/webshell -run 'Prevention|Stats' -count=1 -p 1 -v
go test ./tests/e2e -run 'TestCLIPreventionLoop|TestToolboxStatistics' -count=1 -p 1 -v -timeout 15m
```

## 4. 备份、恢复、删除和升级

- [ ] `database.BusinessTables` 按依赖顺序加入四张预防表；备份 manifest、`pg_restore --list` 和恢复行数校验自动覆盖它们。
- [ ] `DeleteCounts` 和 `scanDeleteCounts` 增加四个字段；`internal/webshell/stats.go` 的删除视图与 `ui.FormatDeleteCounts` 同步显示，不静默丢弃新计数。
- [ ] `TestBackupRestoreIntoEmptyDatabase` 在备份前创建 artifact、两个版本、issue 关联和 passed/failed 验证；恢复后逐字段检查状态、哈希、commit、关联、验证和审计。
- [ ] project 删除预览与执行对四类新行返回相同计数，跨 issue 关联不残留，audit_log 保留并新增一个 delete.scope。
- [ ] connector-only、before-only 和 retention 测试证明不会静默删除 active artifact；过期 event/occurrence 删除后指标按剩余真实 occurrence 计算，不伪造历史。
- [ ] 将 `spec/compat.cue` 的升级保证从 0008 更新到 0009，矩阵保留 0007→0009 直接升级和未来版本拒绝；`db/migrations/README.md` 同步说明。
- [ ] 重跑批次一的 0007→0009、0010 故障回滚测试，确认最终文档与实际迁移一致。

运维验收：

```powershell
go test ./internal/database -run 'TestBackupRestoreIntoEmptyDatabase|TestDelete|TestRetention|TestUpgrade' -count=1 -p 1 -v -timeout 15m
go test ./internal/ui ./internal/webshell -run 'Delete|Settings|Stats' -count=1 -v
```

预期：真实 pg_dump/pg_restore 无 SKIP，四张表和约束完整恢复，预览与执行计数一致。

## 5. 双语文档

- [ ] 新增中英文 Prevention 指南，包含四种产物、状态机、六个 CLI 命令、三个 MCP 工具、Git 前置条件、指标定义、失败排查和完整示例。
- [ ] 明确 verify/activate 是人工 CLI 决策，MCP 的 register 不读取文件也不证明哈希；Web 只读。
- [ ] README 的功能总览、命令、MCP 数量和文档索引同步；当前 v0.1.3 发布状态不改写，新能力标为源码中的 Unreleased。
- [ ] 功能实现说明增加实际调用链和文件映射；项目状态区分“已实现/已验证/未发布”，不写虚构发布日期或 tag。
- [ ] 隐私文档说明 1 MiB 上限、正文不入库、仓库绝对路径不入库、argv/证据脱敏、有界输出和环境白名单。
- [ ] `CHANGELOG.md` 只更新 Unreleased，列出新增能力、0009 升级、运维兼容和已知限制；中英文语义逐项对齐。

## 6. 最终全量验收

- [ ] 所有本批聚焦测试通过后，运行完整质量门禁；数据库测试不得因缺环境而跳过。

```powershell
gofmt -w cmd/errkb internal tests/e2e
go vet ./...
go test ./... -count=1 -p 1 -timeout 20m
cue vet ./spec/...
cue export ./spec -e compat --out json
pnpm run typecheck
pnpm run lint
pnpm run format:check
pnpm test
pnpm run build
git diff --check
```

- [ ] 在 PR/CI 中确认 `compat` job 的 19 条规则自检和 R10/R5 分类通过；本地 `cue export` 不能替代该完整兼容门禁。
- [ ] 运行 `./packaging/windows/build.ps1 -Version 0.0.0-prevention-acceptance`，再执行 `./dist/errkb.exe version` 和 `./dist/errkb.exe mcp selftest`；这是本地验收构建，不是 Release。
- [ ] 最终人工核对端到端证据：真实 Git 状态、真实 PostgreSQL 行、MCP 12 工具、Web 只读区域、备份恢复和无 SKIP 日志。
- [ ] 对照设计规格逐项检查非目标：没有自动测试生成、自动改文件、自动 Git 写、模型调用、外网、团队云功能或虚构预防次数。

## 7. 交接完成

交接内容不是“代码已写”，而是：五批测试证据、0009 升级/回滚证据、真实 CLI/Git 闭环、MCP/Web 边界、恢复与删除结果、双语 Unreleased 文档。后续是否提交、推送、编号发布，由项目所有者另行决定。
