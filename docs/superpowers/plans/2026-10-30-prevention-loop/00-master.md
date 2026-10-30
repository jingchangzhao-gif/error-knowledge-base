# Local Error Prevention Loop: Staged Implementation Plan

Date: 2026-10-30  
Status: implementation plan; coding has not started  
Design source: `docs/superpowers/specs/2026-10-30-local-error-prevention-loop-design.md`

## 1. How to use these plans

- Submit one batch file at a time. The executor implements only that batch and does not change later batches early.
- Run batches strictly in `01 → 02 → 03 → 04 → 05` order. Start the next batch only after every test and handoff item in the previous batch passes.
- Write failing tests first, then the minimum implementation, then run the batch acceptance commands. A skipped database or Git integration test does not count as acceptance.
- Do not create Git commits, push, tag, or publish a release. Those actions require a separate explicit owner request.
- Implement sequentially in the current session without subagents.

## 2. Fixed product boundaries

The first version delivers only:

```text
resolved issue → register draft → explicit user verify → explicit user activate
→ bind Git HEAD → observe real recurrence
```

The following constraints are mandatory:

- Single local developer; PostgreSQL stores only metadata, links, verifications, activations, and audit records.
- Artifact bodies live only in the user's Git repository. Neither the database nor audit stores bodies or absolute repository paths.
- Four kinds: `regression_test`, `deterministic_check`, `agent_instruction`, `intent`.
- Four version states: `draft`, `verified`, `active`, `deprecated`.
- CLI is the only verify/activate entry point. MCP can register drafts and query. Web is read-only.
- No test generation, project-file edits, automatic Git writes, model calls, or external network access.
- Never display `prevented_count` or turn an absence of recurrence into an invented number of prevented errors.

## 3. Shared cross-batch contract

Every batch uses these names; do not introduce synonyms.

### 3.1 Local API

`schema_version` is always `v1`. Operations:

- `prevention_add`
- `prevention_verify`
- `prevention_activate`
- `prevention_list`
- `prevention_get`
- `prevention_stats`

Go types live in `internal/contract/prevention.go`; CUE types live in `spec/prevention.cue`.

| Go type | Required meaning |
| --- | --- |
| `PreventionAddRequest` | `issue_id`, `kind`, `title`, `artifact_path`, `content_sha256`, optional `source`/`actor` |
| `PreventionAddResponse` | `artifact`, `version`, `created`; duplicate content returns the existing version with `created=false` |
| `PreventionVerifyRequest` | `artifact_version_id`, `repo_path`, `command []string`; repository path is wire-only and never persisted |
| `PreventionVerifyResponse` | `verification`, `version`; failed verification is a valid response with `verification.passed=false` |
| `PreventionActivateRequest` | `artifact_version_id`, `repo_path` |
| `PreventionActivateResponse` | `version`, `activated`; same-version/same-commit retry returns `activated=false` |
| `PreventionListRequest` | optional `issue_id`, `project_key`, `kind`, `status`, `limit`, `cursor` |
| `PreventionListResponse` | `items []PreventionArtifactView`, optional `next_cursor` |
| `PreventionGetRequest` | `artifact_id` |
| `PreventionGetResponse` | `artifact`, `versions`, `verifications` |
| `PreventionStatsRequest` | optional `project_key`, `issue_id` |
| `PreventionStatsResponse` | state/kind counts, verification pass rate, activation observations, and mature protection rate |

Shared read shapes:

- `PreventionArtifact`: `artifact_id`, `project_key`, `kind`, `title`, `issue_ids`, `created_at`, `updated_at`.
- `PreventionVersion`: `artifact_version_id`, `artifact_id`, `version`, `artifact_path`, `content_sha256`, `status`, optional `commit_sha`/`activated_at`, `created_at`, optional `latest_verification`.
- `PreventionVerification`: `verification_id`, `artifact_version_id`, redacted `command`, nullable `exit_code`, `passed`, `duration_ms`, bounded redacted `evidence`, whitelisted `environment`, `content_sha256`, `created_at`.
- `PreventionArtifactView`: `artifact` and its `versions`.

Statistics shapes:

- `PreventionCount`: `name`, `count`, used by both `by_status` and `by_kind`.
- `PreventionActivationStats`: `issue_id`, `artifact_id`, `artifact_version_id`, `activated_at`, `commit_sha`, `status`, `first_seen_to_activation_ms`, `recurrence_count`, `before_count`, `after_count`, `before_days`, `after_days`, `observation_status`.
- `observation_status` is only `observing` or `mature`.
- `PreventionStatsResponse` also contains `verification_runs`, `verification_passed`, `verification_pass_rate`, `mature_activations`, `mature_zero_recurrence`, nullable `mature_protection_rate`, and `recurring_version_ids`.

### 3.2 Database functions and audit

`internal/database/prevention.go` provides:

- `RegisterPrevention`
- `GetPreventionVersion`
- `RecordPreventionVerification`
- `ActivatePrevention`
- `ListPreventions`
- `GetPrevention`
- `PreventionStats` (batch 5)

Audit actions:

- `prevention.artifact.create`
- `prevention.issue.link`
- `prevention.version.create`
- `prevention.verify`
- `prevention.activate`

### 3.3 State and idempotency interpretations

- Registration locates an artifact by `(issue_id, kind, artifact_path)`. The same path and SHA-256 returns the latest existing version regardless of state, without appending a version or writing audit. A different SHA-256 appends a draft.
- A version can be activated at most once. An active version retried at the same commit is idempotent. A different commit or a deprecated version requires a new draft; revival is forbidden.
- A verified version can be verified again. Every run appends verification and audit records. A latest failure does not regress state but blocks activation.
- Activating a new version deprecates the old active version in the same transaction while retaining its `commit_sha` and `activated_at` for historical metrics.
- Each version stores one activation proof, so every metric activation corresponds exactly to one `(issue_id, artifact_version_id)`; no activation-history table is needed.

## 4. Dependencies and handoffs

| Batch | File | Delivery | Exit condition |
| --- | --- | --- | --- |
| 1 | `01-contracts-and-migration.md` | v1 contracts, Go validation, 0009 tables/constraints/indexes/delete functions, upgrade tests | CUE, Go contract, real PostgreSQL migration and upgrade tests pass |
| 2 | `02-core-service-and-state-machine.md` | Data access, state machine, audit, file hashing, explicit command execution, read-only Git, backend dispatch | Service/database tests cover concurrency and failures against real PostgreSQL |
| 3 | `03-cli-and-git-e2e.md` | Five CLI subcommands, pipe client, long verification timeout, real CLI/Git E2E | add→verify→activate→recurrence works; stats arrives in batch 5 |
| 4 | `04-mcp-and-web-readonly.md` | Three MCP tools, issue Preventions section, read-only HTTP query | No MCP verify/activate and no Web prevention writes |
| 5 | `05-metrics-operations-release.md` | Recurrence metrics, stats page, backup/delete/upgrade compatibility, bilingual docs, full acceptance | All quality gates and real recovery/E2E tests pass |

## 5. File ownership

- Batch 1: `spec/prevention.cue`, `spec/local-api.cue`, `spec/mcp-tools.cue`, `spec/compat.cue`, `internal/contract/prevention.go`, `internal/contract/local_api.go`, `db/migrations/0009_prevention.sql`, and tests.
- Batch 2: `internal/database/prevention.go`, `internal/service/prevention.go`, `internal/service/api.go`, `internal/service/backend.go`, `internal/capture/run.go`, and tests.
- Batch 3: `cmd/errkb/main.go`, `cmd/errkb/cli.go`, new `cmd/errkb/prevention.go`, `internal/service/client.go`, `tests/e2e/prevention_loop_test.go`, and tests.
- Batch 4: `internal/mcp/tools.go`, `internal/webshell/server.go`, `internal/webshell/detail.go`, new `internal/webshell/prevention.go`, `internal/webshell/assets/app.js`, `cmd/errkb/web.go`, and tests.
- Batch 5: `internal/database/prevention.go`, `internal/database/backup.go`, `internal/webshell/stats.go`, `internal/ui/settings.go`, stats frontend, operations tests, and bilingual documentation.

Shared files may receive only the capability assigned to the current batch. If accepted public semantics must change, stop and revise the master plan and design specification first.

## 6. Acceptance environment

- Windows PowerShell with the repository root as the working directory.
- Go follows `go.mod`, CUE is CI-pinned `v0.17.1`, and Node/pnpm follow `package.json`.
- Real PostgreSQL 16 acceptance requires `ERRKB_TEST_DATABASE_URL`; tests use private schemas or isolated databases.
- Git E2E tests operate only on temporary repositories under `t.TempDir()`, never on the user's index, commits, or branches.
- Actual exit codes decide success. Required integration tests reporting `SKIP` must be rerun with the environment configured.

## 7. Final definition of done

The prevention loop is complete only when:

1. Every checkbox in all five batches is complete, without later batches fabricating earlier evidence.
2. `go test ./... -count=1 -p 1 -timeout 20m`, `go vet ./...`, `cue vet ./spec/...`, and existing TypeScript checks pass.
3. Real PostgreSQL proves 0008→0009 upgrade, failed-migration rollback, backup/restore, and scoped deletion.
4. A real temporary Git repository proves registration, passing verification, activation, dirty-path rejection, and post-activation recurrence metrics.
5. MCP exposes only registration/query; Web has no verify, activate, command execution, or Git write entry point.
6. English and Chinese READMEs, implementation notes, project status, privacy documentation, and the Unreleased changelog agree without claiming a release early.
