# Batch 4: MCP Draft/Read-Only APIs and Web Read-Only Presentation

Prerequisite: batch 3 acceptance is complete.  
Goal: add three MCP tools and show Preventions on issue detail. No prevention statistics and no Web writes.

## 1. Files in scope

Create:

- `internal/mcp/prevention.go`
- `internal/mcp/prevention_test.go`
- `internal/webshell/prevention.go`
- `internal/webshell/prevention_test.go`

Modify:

- `internal/mcp/tools.go`
- `internal/mcp/tools_test.go`
- `cmd/errkb/mcp_test.go`
- `internal/webshell/server.go`
- `internal/webshell/detail.go`
- `internal/webshell/detail_test.go`
- `internal/webshell/assets/app.js`
- `cmd/errkb/web.go`
- `tests/e2e/ui_detail_test.go`

Do not change `get_issue v1`, the database, CLI verify/activate, MCP protocol envelope, or stats structures.

## 2. Three MCP tools

- [ ] Extend tool-list tests first, then add `PreventionAdd`, `PreventionList`, and `PreventionGet` to `mcp.Backend`. Reuse the batch 3 client and the same `SQLBackend`; do not create another database path.
- [ ] Append `preventionRegisterTool`, `preventionListTool`, and `preventionGetTool` to `NewTools`, increasing the total from 9 to 12. Names, read_only values, and inputs match batch 1 CUE.
- [ ] `err_prevention_register` requires `issue_id`, `kind`, `title`, `artifact_path`, and `content_sha256`. It has no `repo_path`, file read, command, or state input.
- [ ] Before calling `PreventionAdd`, register fixes `source="mcp"` and `actor="mcp"`. New writes are draft only; idempotent hits return current state without changing it.
- [ ] `err_prevention_list` supports issue/project/kind/status/limit/cursor. `err_prevention_get` accepts only artifact id. Both have `ReadOnly=true`.
- [ ] All three use strict argument decoding, request validation, backend call, and response validation. Only an empty `schema_version` defaults to v1; an explicitly wrong version is rejected.
- [ ] Update `CorrectionNotice`: MCP can register prevention drafts and query, but only a user running CLI can verify/activate. Never imply an Agent verified or activated anything.

Negative MCP boundary tests:

- [ ] `tools/list` contains exactly 12 tools, including the three new tools, and no tool whose name or description offers verify/activate execution.
- [ ] Register rejects `status=verified/active`, `repo_path`, `command`, file body, and unknown fields.
- [ ] A recording backend fake proves MCP never calls `PreventionVerify` or `PreventionActivate`; those methods are absent from `mcp.Backend` itself.
- [ ] Results cannot claim the core read and proved the content hash. Descriptions say the caller computed it and mechanical proof still requires CLI verify.

## 3. MCP selftest

- [ ] Update `cmd/errkb/mcp_test.go` and selftest expectations; initialization instructions include the human CLI boundary.
- [ ] Invalid kind/hash/path returns invalid-params; backend conflict/not_found retains existing JSON-RPC mapping.
- [ ] Existing nine tools retain their behavior, especially ErrorEvent, trust, and solution-write contracts.

Commands:

```powershell
go test ./internal/mcp -run 'Prevention|Tools|Tool' -count=1 -v
go test ./cmd/errkb -run 'MCP|Prevention' -count=1 -v
```

## 4. Read-only Web query

- [ ] Add `webshell.PreventionListFunc` and `Server.EnablePreventions` without changing the `NewWithDetail` signature, preserving existing search/detail startup.
- [ ] Add `GET /api/issues/{id}/preventions`: validate token and UUID, call `PreventionListRequest{issue_id=id}`, and return the contract response.
- [ ] Inject through `client.PreventionList` in `cmd/errkb/web.go`. Do not connect directly to PostgreSQL or duplicate filtering/state rules.
- [ ] Add no prevention POST/PUT/PATCH/DELETE route, command execution, repository-path input, credential input, or Git operation.
- [ ] Keep `get_issue` and `IssueDetailV` unchanged so old strict v1 readers receive no new field.

Web API tests:

- [ ] 200 returns linked artifacts/versions; no links returns empty items; missing token returns 401; invalid UUID returns 404; unwired returns 503; backend failure returns 502.
- [ ] The route table has no prevention writes, and existing CSP, `form-action 'none'`, loopback, and token behavior remain unchanged.

## 5. Issue-detail Preventions section

- [ ] `loadDetail` fetches issue detail and preventions in parallel. A prevention-query failure shows an independent error without hiding the original issue.
- [ ] Add `renderPreventions` for kind, title, version, state, path, latest verification, activation commit, and verify/activate CLI instructions.
- [ ] Draft shows verify, verified shows activate, active shows HEAD and activation time, and deprecated shows history only. Instructions never guess an absolute repository path from the database.
- [ ] Continue escaping all text with `escapeText` and URL-encoding ids. Never concatenate unescaped data into `innerHTML`.
- [ ] No Verify/Activate buttons, automatic fixes, test generation, or “prevented N times” copy.
- [ ] Extend `tests/e2e/ui_detail_test.go` to check detail and read-only prevention APIs through a real daemon/client. Do not claim the stats page is complete.

Commands:

```powershell
go test ./internal/webshell -run 'Prevention|Detail|Server' -count=1 -v
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 4 acceptance' }
go test ./tests/e2e -run 'TestToolboxDetail' -count=1 -p 1 -v
```

## 6. Completion and handoff

- [ ] Run `gofmt` on batch Go files, then `go test ./internal/mcp ./internal/webshell ./cmd/errkb -count=1 -p 1`.
- [ ] Run `go vet ./internal/mcp ./internal/webshell ./cmd/errkb`, `pnpm exec prettier --check internal/webshell/assets/app.js`, and `git diff --check`.
- [ ] Confirm MCP has no verify/activate tool, Web has only read-only prevention GET, and no body storage, command execution, or automatic state change exists.

Handoff to batch 5: reusable list/get presentation data and a 12-tool MCP surface. Stats command, stats API, and stats page remain unimplemented.
