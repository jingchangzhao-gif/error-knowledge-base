# 批次四：MCP 草稿/只读接口与 Web 只读呈现

前置：批次三全部验收通过。  
本批目标：新增三个 MCP 工具，并在 issue 详情展示 Preventions；不实现预防统计，不开放任何 Web 写操作。

## 1. 允许修改的文件

新增：

- `internal/mcp/prevention.go`
- `internal/mcp/prevention_test.go`
- `internal/webshell/prevention.go`
- `internal/webshell/prevention_test.go`

修改：

- `internal/mcp/tools.go`
- `internal/mcp/tools_test.go`
- `cmd/errkb/mcp_test.go`
- `internal/webshell/server.go`
- `internal/webshell/detail.go`
- `internal/webshell/detail_test.go`
- `internal/webshell/assets/app.js`
- `cmd/errkb/web.go`
- `tests/e2e/ui_detail_test.go`

不得修改 `get_issue v1`、数据库、CLI verify/activate、MCP 协议 envelope 或统计结构。

## 2. MCP 三个工具

- [ ] 先扩展工具表测试，再为 `mcp.Backend` 添加 `PreventionAdd`、`PreventionList`、`PreventionGet`；复用批次三 client 和同一 `SQLBackend`，不新建数据库路径。
- [ ] `NewTools` 追加 `preventionRegisterTool`、`preventionListTool`、`preventionGetTool`，总数由 9 变为 12；描述符名称、read_only 和输入字段与批次一 CUE 一致。
- [ ] `err_prevention_register` 的必填输入为 `issue_id`、`kind`、`title`、`artifact_path`、`content_sha256`；不要求 `repo_path`，不读取文件，不接受命令或状态字段。
- [ ] register 调用前由工具固定 `source="mcp"`、`actor="mcp"`，调用 `PreventionAdd`。新写入只能是 draft；幂等命中已有版本只返回现状，不改变状态。
- [ ] `err_prevention_list` 支持 issue/project/kind/status/limit/cursor；`err_prevention_get` 只接受 artifact id。两者 `ReadOnly=true`。
- [ ] 三个工具都经过严格参数解码、请求 `Validate()`、backend 调用和响应 `Validate()`；空 `schema_version` 才默认 v1，显式错误版本必须拒绝。
- [ ] 更新 `CorrectionNotice`：MCP 可登记预防草稿和查询，但验证/激活只能由用户通过 CLI 完成；不得暗示 Agent 已验证或已激活。

MCP 负向边界测试：

- [ ] `tools/list` 精确包含 12 个工具，包含三个新工具，不包含任何名称或描述可执行 verify/activate 的工具。
- [ ] register 不能传入 `status=verified/active`、`repo_path`、`command`、文件正文或未知字段。
- [ ] backend fake 记录所有调用，断言 MCP 从未调用 `PreventionVerify` 或 `PreventionActivate`；`mcp.Backend` 本身也不包含这两个方法。
- [ ] 工具结果不能声称内容哈希已经由核心读取验证；描述明确哈希由调用方计算，机械证据仍须 CLI verify。

## 3. MCP 自检

- [ ] 更新 `cmd/errkb/mcp_test.go` 和 selftest 期望，初始化说明包含人工 CLI 边界。
- [ ] 测试非法 kind/hash/path 返回 invalid-params，backend conflict/not_found 保留现有 JSON-RPC 映射。
- [ ] 验证既有九个工具行为不变，尤其是 ErrorEvent、可信度和解法写入契约。

命令：

```powershell
go test ./internal/mcp -run 'Prevention|Tools|Tool' -count=1 -v
go test ./cmd/errkb -run 'MCP|Prevention' -count=1 -v
```

## 4. Web 只读查询

- [ ] 新增 `webshell.PreventionListFunc` 和 `Server.EnablePreventions`，不改变 `NewWithDetail` 签名，既有搜索/详情启动方式保持兼容。
- [ ] 新增 `GET /api/issues/{id}/preventions`：校验 token 和 UUID，调用 `PreventionListRequest{issue_id=id}`，直接返回契约响应。
- [ ] 在 `cmd/errkb/web.go` 通过 `client.PreventionList` 注入；不直接连接 PostgreSQL，不复制过滤或状态规则。
- [ ] 不新增 prevention POST/PUT/PATCH/DELETE 路由，不新增命令执行、仓库路径输入、凭据输入或 Git 操作。
- [ ] `get_issue` 和 `IssueDetailV` 保持不变，避免给旧 v1 严格读者增加字段。

Web API 测试：

- [ ] 200 返回关联 artifact/版本；无关联返回空 items；无 token 返回 401；非法 UUID 返回 404；未注入返回 503；backend 故障返回 502。
- [ ] 路由表没有 prevention 写路由，既有 CSP、`form-action 'none'`、loopback 和 token 机制不变。

## 5. issue 详情 Preventions 区域

- [ ] `loadDetail` 并行请求原 issue 详情和 preventions；预防查询失败时显示独立错误，不让原 issue 详情消失。
- [ ] 新增 `renderPreventions`，展示类型、标题、版本、状态、路径、最近验证、激活 commit 和验证/激活 CLI 提示。
- [ ] draft 显示 verify 提示，verified 显示 activate 提示，active 显示 HEAD 和激活时间，deprecated 只显示历史；提示不包含从数据库猜测的仓库绝对路径。
- [ ] 所有文本继续经 `escapeText`；id 放入 URL 时经 `encodeURIComponent`；不使用 `innerHTML` 直接拼接未转义数据。
- [ ] 不显示 Verify/Activate 按钮，不提供自动修复、生成测试或“已预防 N 次”的文案。
- [ ] 扩展 `tests/e2e/ui_detail_test.go`，用真实 daemon/client 检查详情接口和只读预防接口；本批不声称完成统计页。

命令：

```powershell
go test ./internal/webshell -run 'Prevention|Detail|Server' -count=1 -v
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 4 acceptance' }
go test ./tests/e2e -run 'TestToolboxDetail' -count=1 -p 1 -v
```

## 6. 本批完成与交接

- [ ] 运行 `gofmt` 处理本批 Go 文件，再运行 `go test ./internal/mcp ./internal/webshell ./cmd/errkb -count=1 -p 1`。
- [ ] 运行 `go vet ./internal/mcp ./internal/webshell ./cmd/errkb`、`pnpm exec prettier --check internal/webshell/assets/app.js` 和 `git diff --check`。
- [ ] 自检：MCP 工具面不含 verify/activate；Web 只有只读预防 GET；没有正文入库、命令执行或自动状态变化。

交接给批次五：可复用的 Prevention list/get 展示数据和 12 工具 MCP 表面；统计命令、统计 API 和统计页仍待实现。
