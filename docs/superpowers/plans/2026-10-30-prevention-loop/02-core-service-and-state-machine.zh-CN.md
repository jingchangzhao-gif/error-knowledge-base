# 批次二：Go 预防服务、状态机、审计和数据库测试

前置：批次一全部验收通过。  
本批目标：实现 add/verify/activate/list/get 的核心与本地 API 后端，不提供 CLI、MCP、Web 或统计命令。

## 1. 允许修改的文件

新增：

- `internal/database/prevention.go`
- `internal/database/prevention_test.go`
- `internal/service/prevention.go`
- `internal/service/prevention_test.go`
- `internal/service/prevention_api_test.go`

修改：

- `internal/service/api.go`
- `internal/service/backend.go`
- `internal/service/pipe_test.go` 和其他因 `Backend` 扩展而需要同步的现有测试 fake
- `internal/capture/run.go`
- `internal/capture/run_test.go`

不得修改 CLI、MCP、Web 和统计结构；不得调用模型或引入新依赖。

## 2. 数据访问与登记事务

- [ ] 先写数据库失败测试，再定义 `database.PreventionArtifact`、`PreventionVersion`、`PreventionVerification`、`PreventionFilter`、`PreventionRegistration`、`PreventionRegistrationResult`、`PreventionVerificationInput`。
- [ ] 实现主计划约定的 `RegisterPrevention`、`GetPreventionVersion`、`RecordPreventionVerification`、`ActivatePrevention`、`ListPreventions`、`GetPrevention`；`PreventionStats` 留到批次五，本批不写空实现。
- [ ] `RegisterPrevention` 在单事务内锁定 `(issue_id, kind, artifact_path)` 的 advisory transaction key，要求 issue 存在、状态为 `resolved` 且项目与 artifact 一致。
- [ ] 同路径同哈希返回最新既有版本，`created=false`，不写新业务行或审计；不同哈希取 `max(version)+1` 追加 draft，`created=true`。
- [ ] 新建 artifact、issue 关联、版本分别写 `prevention.artifact.create`、`prevention.issue.link`、`prevention.version.create`；同事务失败不得留下半条登记。
- [ ] 标题、source、actor 在写入前使用现有 redaction 引擎；数据库不接收正文和 `repo_path`。
- [ ] 列表按稳定 `(updated_at, artifact_id)` 游标分页，过滤 issue/project/kind/status；详情按版本号排序，验证按时间排序，空结果返回非 nil 空数组。

登记测试：

- [ ] open/wontfix/misjudged issue 拒绝，resolved issue 成功。
- [ ] 重复登记幂等且审计数不变；不同哈希只新增 draft，不影响既有 active。
- [ ] 两个并发登记同一键只产生一个 artifact、一个关联和一个版本。
- [ ] artifact 可关联多个 issue，跨项目关联和不存在 issue 被外键或服务校验拒绝。

## 3. 验证状态机

- [ ] `RecordPreventionVerification` 锁定目标版本和 artifact，只接受 draft/verified；active/deprecated 返回冲突。
- [ ] 每次运行追加一条 `prevention_verifications` 和一条 `prevention.verify` 审计，不覆盖历史，不做验证幂等。
- [ ] passed 时版本进入 verified；失败时保持原 draft/verified 状态。verified 后再次失败仍可读取，但最近记录为 failed。
- [ ] 验证记录的 `content_sha256` 必须等于版本登记哈希；`command`、`evidence` 必须在写入前脱敏且有界，`environment` 只包含白名单摘要。
- [ ] 非零退出、启动失败、超时和验证前后哈希变化均形成 failed 记录；登记前哈希不匹配则在执行命令前拒绝且不写验证记录。

状态机测试：

- [ ] draft→verified→active→deprecated 的合法路径，以及 active/deprecated 再验证、deprecated 复活、直接 activate draft 的拒绝。
- [ ] verified 重验失败后 activate 拒绝；再次 passed 后允许 activate。
- [ ] 验证运行次数、最近结果、退出码和审计逐次追加。

## 4. 激活事务

- [ ] `ActivatePrevention` 在单事务内锁定 artifact 和目标版本，要求 verified、最近一次验证 passed、最近验证 SHA 与登记 SHA 相同。
- [ ] active 且 commit 相同的重复调用返回原版本和 `activated=false`，不新增审计；active 但 commit 不同返回冲突。
- [ ] 激活新版本时，旧 active 在同一事务变 deprecated，目标写 `active`、40 位 commit 和激活时间，并追加 `prevention.activate` 审计。
- [ ] 任何校验或审计失败都回滚，旧 active 不变；deprecated 永远不接受重新激活。
- [ ] 并发激活两个版本最终最多一个 active；失败方返回冲突或完整重试后的确定状态，不出现双 active 或审计半提交。

## 5. 文件、命令和只读 Git 执行层

- [ ] 在 `internal/service/prevention.go` 实现 `resolvePreventionFile`：规范化仓库根和斜杠相对路径，拒绝越界、`.git`、符号链接、非普通文件和大于 1 MiB 的文件；同时导出 `HashPreventionArtifact(repo, path)`，供批次三 CLI add 复用同一套校验与哈希逻辑。
- [ ] add 的核心只登记调用方提交的哈希；verify/activate 的核心必须从 `repo_path + artifact_path` 重新读取文件并计算 SHA-256，不能信任请求另带的哈希。
- [ ] verify 使用 `capture.Run`，`Dir` 为仓库根，`Recorder=nil`，直接 argv 启动，不经过 `cmd /c`、PowerShell 或 shell 拼接。
- [ ] 默认验证超时 120 秒；stdout/stderr 各复用现有 64 KiB 头尾捕获，最终 evidence 用 `redact.Sample` 处理并包含截断/超时/启动失败说明。
- [ ] 将 `capture.summarizeEnvironment` 导出为 `capture.SummarizeEnvironment` 并复用，持久化环境只含 OS、架构、shell 基名和 Go 版本；子进程可继承执行环境，但不得 dump 环境变量。
- [ ] 命令结束后再次计算产物哈希；改变产物即 failed，即使命令退出 0。服务不向 CLI 实时转发子进程输出，CLI 后续只展示有界脱敏证据。
- [ ] activate 只调用只读 Git：解析仓库根和 40 位 HEAD、用 `git cat-file -e HEAD:<path>` 证明路径在 HEAD、用 `git status --porcelain=v1 --untracked-files=all -- <path>` 证明该路径无未提交变化。
- [ ] 所有 Git 调用使用 argv 和有界输出；拒绝非 Git 仓库、未跟踪路径、仅暂存未提交、工作区修改和异常 HEAD。禁止 checkout/add/commit/push/config 等写操作。

执行层测试：

- [ ] 使用当前测试二进制的 helper process 产生退出 0、非零、大量输出、敏感样本、休眠和修改产物的行为，覆盖超时与证据脱敏。
- [ ] 使用 `t.TempDir()` 的真实 Git 仓库覆盖 tracked clean、untracked、staged、modified、非仓库和路径越界；测试配置的 identity 只写入临时仓库。
- [ ] 比较 activate 前后临时仓库 HEAD、索引和工作区内容，证明核心没有执行 Git 写操作。

## 6. 本地 API 后端

- [ ] 扩展 `service.Backend` 和 `API.Handle`，只接入 `prevention_add`、`prevention_verify`、`prevention_activate`、`prevention_list`、`prevention_get`；stats 在批次五接入。
- [ ] 在 `SQLBackend` 上实现五个方法：先 `Validate()`，再经 `withConn` 或池事务调用核心，最后映射为批次一契约并再次校验。
- [ ] CLI 与 MCP 共用 add/list/get，不在 transport 复制状态机。MCP 尚未暴露，因此本批 verify/activate 仍无用户入口。
- [ ] 在 `translateBackendError` 统一映射：不存在→`not_found`，状态/哈希/Git 冲突→`conflict`，结构校验→`invalid_request`，连接失败→`unavailable`，未知错误→`internal`。
- [ ] 验证失败是正常 `PreventionVerifyResponse`，不是 pipe error；命令无法启动或超时也落 failed 记录后返回该响应。
- [ ] 为响应映射写测试，确保 `repo_path`、文件正文、原始环境变量和未脱敏输出不出现在响应中。

## 7. 验收与交接

先运行聚焦测试：

```powershell
go test ./internal/capture -run 'TestRun|Environment' -count=1 -v
go test ./internal/service -run 'Prevention|Pipe' -count=1 -v
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 2 acceptance' }
go test ./internal/database -run 'Prevention' -count=1 -p 1 -v
go test ./internal/service -run 'Prevention' -count=1 -p 1 -v
```

再运行回归：

```powershell
gofmt -w internal/database/prevention.go internal/database/prevention_test.go internal/service/prevention.go internal/service/prevention_test.go internal/service/prevention_api_test.go internal/service/api.go internal/service/backend.go internal/capture/run.go internal/capture/run_test.go
go test ./internal/contract ./internal/capture ./internal/service ./internal/database -count=1 -p 1
go vet ./internal/...
git diff --check
```

预期：必需 PostgreSQL/Git 测试无 SKIP；所有失败路径都有明确错误且旧 active 保留；没有任何 CLI/MCP/Web 验证或激活入口。

交接给批次三：五个可调用的 `SQLBackend` 方法、稳定错误码、真实文件/命令/Git 能力和数据库测试夹具。
