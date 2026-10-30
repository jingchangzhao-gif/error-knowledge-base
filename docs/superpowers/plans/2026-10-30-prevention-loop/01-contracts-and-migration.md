# Batch 1: Contracts, Compatibility Rules, and Migration 0009

Prerequisite: read `00-master.md` and the design specification.  
Goal: fix the Prevention v1 wire fields and database structure without implementing services, CLI, MCP, or Web.

## 1. Files in scope

Create:

- `spec/prevention.cue`
- `internal/contract/prevention.go`
- `internal/contract/prevention_test.go`
- `internal/contract/testdata/prevention/add-valid.json`
- `internal/contract/testdata/prevention/register-valid.json`
- `db/migrations/0009_prevention.sql`

Modify:

- `spec/local-api.cue`
- `spec/mcp-tools.cue`
- `spec/compat.cue`
- `internal/contract/local_api.go`
- `internal/database/migrate_test.go`
- `internal/database/upgrade_test.go`
- `cmd/errkb/db_test.go` (migration-count regression assertion only; no CLI implementation)

Do not change published migrations `0001`–`0008`, and do not start `internal/service` or `cmd/errkb` implementation.

## 2. Contracts and validation

- [x] Write `TestPreventionContracts` first. Cover all six requests/responses from the master plan (`PreventionAddRequest`/`PreventionAddResponse` through `PreventionStatsRequest`/`PreventionStatsResponse`), empty arrays, nullable fields, unknown fields, wrong versions, and every enum.
- [x] In `spec/prevention.cue`, define `#PreventionSchemaVersion: "v1"`, four `#PreventionKind` values, four `#PreventionStatus` values, two `#PreventionObservationStatus` values, and every request, response, and shared shape from the master plan.
- [x] Define `#PreventionRegisterTool`, `#PreventionListTool`, and `#PreventionGetTool`, named `err_prevention_register`, `err_prevention_list`, and `err_prevention_get`. Do not define verify/activate tools.
- [x] `artifact_path` is a repository-relative slash path. Reject empty values, absolute paths, drive letters, backslashes, empty segments, `.`, `..`, and any `.git` segment. `content_sha256` is 64 lowercase hex characters; `commit_sha` is 40.
- [x] `PreventionVerifyRequest.command` contains at least one non-empty argv element and is not a shell string. `repo_path` is required on the wire but absent from every response and persistence shape.
- [x] List `limit` is 1–100; rates are 0–1; counts, durations, and effective days are non-negative; `exit_code` is a pointer so start failure/timeout can be null.
- [x] Implement matching Go types and `Validate()` methods in `internal/contract/prevention.go`. Use a dedicated `PreventionSchemaVersion`, not the ErrorEvent constant.
- [x] Apply existing `checkRedacted` to every persisted free-text field. Request `title`, optional `source`/`actor`, and verification evidence cannot bypass redaction checks.
- [x] Add all six operations to `validOperations`, `validatePayload`, and `validateResult` in `internal/contract/local_api.go`, retaining strict unknown-field rejection through `decodeStrict`.
- [x] Extend `#Operation`, `#ToolRequest`, and `#ToolResponse` in `spec/local-api.cue`; append the three descriptors to `#Tools` in `spec/mcp-tools.cue`.
- [x] Add a `prevention` entry to `spec/compat.cue`, listing definitions, fields, required fields, and enums, bound to `DisallowUnknownFields` in `internal/contract/local_api.go`. Extend `local_api.enums.operation`. Do not change `detail_view v1` or bump existing contract versions.

## 3. Contract tests

- [x] Valid payloads: CLI add, MCP register, verify argv, activate, fully filtered list, get, stats, and empty statistics.
- [x] Invalid payloads: bad kind/status/hash, absolute path, `../`, `.git`, backslash, empty command, negative metrics, and unknown fields. The resolved-issue business precondition is not structural validation and does not belong here.
- [x] Prove `DecodePipeRequest` and `DecodePipeResponse` strictly decode all six operations and reject missing response identities or illegal states.
- [x] Use the two valid JSON fixtures for CLI add and MCP register. Their shapes are identical; caller-computed MCP hashing adds no wire field.

Contract acceptance:

```powershell
go test ./internal/contract -run 'Prevention|Pipe' -count=1 -v
cue vet ./spec/...
cue vet ./spec -d '#PreventionAddRequest' internal/contract/testdata/prevention/add-valid.json
cue vet ./spec -d '#PreventionAddRequest' internal/contract/testdata/prevention/register-valid.json
cue export ./spec -e compat --out json
```

Expected: every command exits 0. Invalid Go cases pass by proving rejection; do not make the overall command depend on an invalid CUE fixture succeeding.

## 4. Migration 0009 structures

- [x] Extend expected tables and structural assertions in `internal/database/migrate_test.go` before creating `0009_prevention.sql`.
- [x] Create `prevention_artifacts`: UUID primary key, `project_key`, `kind`, `title`, `created_at`, `updated_at`, `source`, `actor`.
- [x] Create `prevention_artifact_versions`: UUID primary key, cascading artifact FK, positive `version`, path, SHA-256, state, nullable commit/activation time, creation/source fields, and `unique(artifact_id, version)`.
- [x] Create `issue_preventions`: composite `(issue_id, artifact_id)` primary key, two cascading FKs, and creation/source fields.
- [x] Create `prevention_verifications`: UUID primary key, cascading version FK, redacted command, nullable exit code, passed, non-negative duration/evidence/environment/SHA-256, and creation/source fields.
- [x] Use CHECK constraints for the four kinds, four states, 64/40 lowercase hashes, and non-negative durations. Active requires commit and activation time; draft/verified forbid both.
- [x] Create partial unique index `prevention_one_active_version` on `artifact_id` only where `status='active'`.
- [x] Add a trigger rejecting updates to `artifact_id`, `version`, `artifact_path`, `content_sha256`, and `created_at`, while allowing service transactions to change state and activation proof.
- [x] Add indexes for issue→artifact, artifact→version, version→verification time, activation time, and `occurrences(issue_id, occurred_at)`.
- [x] Add no audit foreign keys; business cascades must never reach audit.

## 5. Scoped deletion in migration 0009

Use `CREATE OR REPLACE FUNCTION` for `errkb_delete_preview` and `errkb_delete_scope` in this same migration, so an accepted migration never needs a later edit.

- [x] A project scope identifies target `prevention_artifacts`; preview and execution add counts for `prevention_verifications`, `issue_preventions`, `prevention_artifact_versions`, and `prevention_artifacts`.
- [x] Delete in this order: verifications, issue links, versions, artifacts, then existing business rows. Cross-issue links follow target artifacts; audit remains.
- [x] Connector-only and before-only scopes do not invent project artifact deletion. Event retention never deletes active artifact metadata.
- [x] Add all four counts to `delete.scope` audit details. Preview and execution return identical counts for the same scope.

## 6. Upgrade and rollback tests

- [x] Add the four tables to `TestMigrateCreatesSchemaV1`; assert columns, CHECKs, partial unique index, immutability trigger, and absence of audit FKs.
- [x] Keep the `schema-0007.sql` fixture. `TestUpgradeFrom0007KeepsTheData` now expects `0008` and `0009`, nine ledger rows, unchanged old-table counts, and empty new tables.
- [x] Continue checking old retrieval `duration_ms=NULL` and fresh event writes after upgrade, rather than checking only table existence.
- [x] Rename the intentionally failing migration in `TestUpgradeRollsBackAFailedMigration` to `0010_broken.sql`. Real `0008`/`0009` must remain, 0010's table and ledger entry must roll back, and retry after deleting 0010 must apply nothing.
- [x] Preserve the semantics of newer-ledger rejection and existing config/Markdown/connector compatibility tests.

Database acceptance with a real `ERRKB_TEST_DATABASE_URL`:

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 1 acceptance' }
go test ./internal/database -run 'TestMigrate|TestSchemaStructure|TestUpgrade' -count=1 -p 1 -v
```

Expected: no SKIP; 0007→0009 preserves data, repeat migration is a no-op, and a 0010 failure does not contaminate 0009.

## 7. Completion and handoff

- [x] Run `gofmt -w internal/contract/prevention.go internal/contract/prevention_test.go internal/contract/local_api.go internal/database/migrate_test.go internal/database/upgrade_test.go`, then rerun every command above.
- [x] Run `go test ./internal/contract ./internal/database -count=1 -p 1` and `git diff --check`.
- [x] Check that no `auto_fix`, `approved_by_agent`, `prevented_count`, persisted absolute repository path, or MCP verify/activate tool exists.

Handoff to batch 2: stable Go/CUE types, six local operation names, four constrained tables, 0009 deletion functions, and reusable real-database fixtures.
