# 批次三：CLI 登记、显式验证、激活及真实 Git 端到端流程

前置：批次二全部验收通过。  
本批目标：交付 `prevention add / verify / activate / list / show` 五个可用命令，并跑通真实 CLI 闭环；`prevention stats` 随批次五的指标一起交付。

## 1. 允许修改的文件

新增：

- `cmd/errkb/prevention.go`
- `cmd/errkb/prevention_test.go`
- `tests/e2e/prevention_loop_test.go`

修改：

- `cmd/errkb/main.go`
- `cmd/errkb/cli.go`
- `cmd/errkb/cli_test.go`
- `internal/service/client.go`
- `internal/service/client_test.go`
- `internal/service/prevention.go`（仅当批次二尚未导出批次三需要的哈希入口时）

不得修改 MCP、Web、数据库结构和统计实现；不得在测试中提交当前项目仓库。

## 2. Pipe client 与超时

- [ ] 先写 client 测试，再为 `service.Client` 添加 `PreventionAdd`、`PreventionVerify`、`PreventionActivate`、`PreventionList`、`PreventionGet` 五个方法；每个都调用对应 operation、解码并 `Validate()`。
- [ ] 扩展 `cmd/errkb/cli.go` 的 `apiClient`，让生产 client 与测试 fake 使用同一五方法表面。
- [ ] 保留普通请求 30 秒超时；新增 `callContextWithTimeout(timeout)`，verify 使用 125 秒 pipe 超时，为核心默认 120 秒执行超时保留 5 秒传输余量。
- [ ] 不为 add/list/show/activate 增加长超时，不让一个卡死的守护进程无限阻塞 shell。
- [ ] client 测试覆盖响应字段、backend error、损坏 JSON、错误 operation 和验证失败但响应合法的情况。

## 3. CLI 参数和输出

- [ ] 在 `runCommand` 增加 `prevention` 分发，在 `usage` 增加命令摘要；`runPrevention` 对未知子命令和缺少子命令返回 `exitUsage=2`。
- [ ] 全局 `--json` 继续可位于命令前后；人类输出和 JSON 都来自同一契约响应，不添加响应外业务字段。

### 3.1 add

```powershell
errkb prevention add --issue <issue-id> --kind regression_test --title "Windows concurrent lock regression" --repo <repository-path> --path tests/windows_lock_regression_test.go
```

- [ ] 五个 flag 均必填；kind 使用四种闭合枚举。
- [ ] 调用批次二核心导出的 `HashPreventionArtifact(repo, path)` 完成路径/文件安全校验和 SHA-256，CLI 不复制另一套路径规则，也不读取正文到数据库。
- [ ] 发送 `PreventionAddRequest`，输出 artifact id、version id、draft 状态和下一步 verify 命令；重复登记显示 `created=false`。

### 3.2 verify

```powershell
errkb prevention verify <version-id> --repo <repository-path> -- go test ./tests -run WindowsLock
```

- [ ] 复用 `splitRunArgs` 在第一个 `--` 分隔 argv；分隔符前只能有一个 version id 和 `--repo`，分隔符后至少一个非空命令参数。
- [ ] 原样发送 argv，不经过 shell，不把命令拼成字符串再解析。
- [ ] 输出 verification id、passed、退出码、耗时、版本状态和有界脱敏证据；passed 时提示 activate。
- [ ] `passed=false` 已落库但 CLI 返回 `exitInternal=1`；参数错误返回 2，daemon 不可用返回 4，不能用成功退出码掩盖失败。

### 3.3 activate

```powershell
errkb prevention activate <version-id> --repo <repository-path>
```

- [ ] 参数只有 version id 和必填 repo；成功输出 active、commit、激活时间及旧版本废弃结果所对应的最新版本视图。
- [ ] 同版本同 commit 重试输出 `activated=false` 并返回 0；哈希变化、脏路径、未跟踪文件和状态冲突返回非零，不能改写旧 active。

### 3.4 list / show

- [ ] `list` 支持 `--issue`、`--project`、`--kind`、`--status`、`--limit`、`--cursor`，无位置参数；显示 artifact、最新版本状态和 next cursor。
- [ ] `show <artifact-id>` 显示 artifact、全部版本、最近验证和激活 commit；不存在返回 `exitNotFound=3`。
- [ ] 草稿提示 verify，verified 提示 activate；提示使用用户提供的仓库路径占位说明，不从数据库猜测绝对路径。

## 4. CLI 单元测试

- [ ] 用 fake `apiClient` 覆盖五个命令的参数解析、`--json`、flags 位于位置参数前后、未知子命令和错误退出码。
- [ ] 验证 verify 不调用 shell：包含空格、引号和 `;` 的 argv 必须逐元素原样到达 fake。
- [ ] 验证 add 只发送相对路径和哈希，请求不含文件正文；临时文件超过 1 MiB、符号链接和 `../` 在拨号前拒绝。
- [ ] 验证 failed verification 输出证据后返回 1，activate 幂等返回 0，list 空页返回 0，show 不存在返回 3。
- [ ] 检查 usage 和 doctor/MCP selftest 既有测试，不把 prevention stats 提前宣称为可用。

聚焦命令：

```powershell
go test ./cmd/errkb -run 'Prevention|Usage' -count=1 -v
go test ./internal/service -run 'Client.*Prevention|Prevention' -count=1 -v
```

## 5. 真实端到端验收

在 `tests/e2e/prevention_loop_test.go` 复用现有 `cli_loop_test.go` 的私有 schema、真实二进制、Named Pipe 和 daemon 测试设施。临时 Git 仓库必须位于 `t.TempDir()`。

- [ ] 先写 `TestCLIPreventionLoop`，未设置 `ERRKB_TEST_DATABASE_URL` 或找不到 Git 时明确跳过；本批正式验收必须配置后运行且无 SKIP。
- [ ] 记录一个失败，将生成的 issue 设为 resolved；在临时仓库创建回归测试文件并用测试进程显式执行 `git add`/`git commit`，这是测试 fixture 准备，不是产品自动 Git 写入。
- [ ] 用编译出的 `errkb.exe prevention add` 登记 draft，断言数据库只有元数据、没有正文和仓库绝对路径。
- [ ] 用 CLI 对临时仓库运行一个真实、确定、退出 0 的验证命令；断言版本 verified、验证行、脱敏证据和审计。
- [ ] 用 CLI activate，断言 active 和当前 40 位 HEAD；再次执行，断言幂等且审计不增加。
- [ ] 修改产物但不提交，再次 activate 必须失败；确认旧 active、commit 和审计保持。
- [ ] 恢复干净文件，再记录一次相同错误，断言新 occurrence 关联原 issue；本批只验证复发事实入库，窗口指标留到批次五。
- [ ] 运行真实非零验证与超时 helper，确认失败记录追加且最近失败阻止后续激活。
- [ ] 全程记录临时仓库 HEAD 和状态，除 fixture 显式准备外，产品命令不得改变 HEAD、索引或工作区。

端到端命令：

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 3 acceptance' }
go test ./tests/e2e -run 'TestCLIPreventionLoop' -count=1 -p 1 -v -timeout 10m
```

## 6. 本批完成与交接

- [ ] 运行 `gofmt` 处理本批 Go 文件，再运行 `go test ./cmd/errkb ./internal/service ./tests/e2e -count=1 -p 1 -timeout 15m`。
- [ ] 运行 `go vet ./cmd/errkb ./internal/service ./tests/e2e` 和 `git diff --check`。
- [ ] 自检：只有 CLI 暴露 verify/activate；没有后台定时验证、自动激活、自动文件修改或产品侧 Git 写操作。

交接给批次四：五个 client 方法和真实 CLI 使用样例；MCP/Web 可复用 add/list/get，但不得转发 verify/activate。
