# Features and implementation guide

[简体中文](features-and-implementation.zh-CN.md)

This document answers two separate questions:

1. What can the Error Knowledge Base do for a user?
2. Which code, contract and database path implements each capability?

It describes the current source tree, not an idealized future release. **Implemented** means the code and tests exist; **integrated** means a current interface can reach the feature; **releasable** additionally requires one coherent tagged artifact to pass the mandatory Windows lifecycle/recovery and provenance gates. See [project-status.md](project-status.md) for the delivery boundary.

## 1. System shape

The project is a local platform, not one large plugin. Host-specific adapters observe failures; one core owns the meaning of an error and the rules for reusing a fix.

```text
DSH plugin     Claude hook     Codex/MCP     CLI wrapper     log/manual input
     \              |              |              |                /
      +-------------+--------------+--------------+---------------+
                                     |
                              ErrorEvent v1
                                     |
                  redact → classify → normalize → fingerprint
                                     |
                       daemon / Named Pipe local API
                                     |
             PostgreSQL + PL/pgSQL search, history and audit
                                     |
                   exact match → fuzzy candidates → rerank
                                     |
                CLI / MCP / tray / local Web toolbox
```

Adapters translate host data; they do not invent their own storage, matching or trust algorithms.

## 2. What each language is responsible for

| Technology          | Responsibility                                                                                                                   | Main locations              |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| Go                  | Runtime core, CLI, daemon, Named Pipe API, capture pipeline, retrieval orchestration, MCP server, native UI models and Web shell | `cmd/errkb/`, `internal/`   |
| PL/pgSQL            | Transactional recording, exact/trigram candidate queries, trust calculation, retention and scoped deletion                       | `db/migrations/`            |
| CUE                 | Canonical contracts for events, configuration, connector capabilities, local API and MCP tools                                   | `spec/`                     |
| TypeScript          | Original DSH plugin, DSH hooks, host-side injection, adapter queue and legacy Markdown fallback                                  | `src/`                      |
| HTML/CSS/JavaScript | Embedded browser presentation for the in-progress Windows toolbox                                                                | `internal/webshell/assets/` |

CUE defines shapes and constraints; it is not the runtime database. PL/pgSQL keeps data operations atomic; it does not own the UI. Go is the central executable. TypeScript remains necessary for the DSH adapter.

## 3. Feature map

| User capability                                   | How it is exposed                                                                   | Main implementation                                                                                                                  | Current state                                                                             |
| ------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------- |
| Capture a failed CLI command                      | `errkb run -- <command>`                                                            | `cmd/errkb/run.go`, `internal/capture/run.go`                                                                                        | Implemented and end-to-end tested                                                         |
| Capture host/tool failures                        | DSH hooks, Claude hook configuration, connector SDK                                 | `src/plugin.ts`, `src/connector.ts`, `internal/capture/connector.go`, `connectors/`                                                  | Implemented; limited to the measured support matrix                                       |
| Capture ordinary logs                             | Tailer library and connector template                                               | `internal/capture/tail.go`, `connectors/templates/cli-log/`                                                                          | Component implemented; live generic-host acceptance incomplete                            |
| Remove secrets before storage                     | Every capture/record path                                                           | `spec/redact.cue`, `internal/redact/`, `src/redact.ts`                                                                               | Implemented and corpus-tested                                                             |
| Classify technical failures                       | Capture pipeline                                                                    | `internal/core/classify.go`, `src/capture.ts`                                                                                        | Implemented for observable technical failures                                             |
| Deduplicate repeated failures                     | Event recording by project + fingerprint                                            | `internal/core/fingerprint.go`, `db/migrations/0002_record.sql`                                                                      | Implemented                                                                               |
| Search known errors                               | CLI, MCP, native omnibox, Web search                                                | `internal/database/search.go`, `candidates.go`, `rerank.go`                                                                          | Implemented across CLI/MCP and the Web toolbox                                            |
| Store and version fixes                           | MCP `err_record_solution`, service/database APIs, Web detail editor                 | `internal/database/solution*.go`, `0003_solutions.sql`                                                                               | Implemented; Web writes use daemon transactions and audit rows                            |
| Learn whether a fix worked                        | `errkb feedback`, MCP `err_feedback`                                                | `internal/database/solution.go`, `errkb_trust()`                                                                                     | Implemented                                                                               |
| Inspect issues and statistics                     | CLI `show`, `list`, `stats`; Web detail/stats/audit pages                           | `cmd/errkb/cli.go`, `internal/service/edit_api.go`, `internal/audit/`, `internal/ui/`, `internal/webshell/`                          | Implemented and automated-test covered                                                    |
| Serve local clients                               | `errkb daemon` over Named Pipe                                                      | `internal/service/daemon.go`, `pipe.go`, `api.go`                                                                                    | Implemented                                                                               |
| Let an LLM actively query the KB                  | `errkb mcp`, twelve tools in Unreleased source                                      | `internal/mcp/`                                                                                                                      | Implemented; MCP is not passive capture                                                   |
| Register, verify and observe prevention artifacts | Six `errkb prevention` commands; three read/register MCP tools; read-only Web stats | `internal/service/prevention.go`, `internal/database/prevention.go`, `internal/mcp/prevention.go`, `internal/webshell/prevention.go` | Implemented and locally tested in Unreleased source; not in v0.1.3                        |
| Diagnose installation/connectors                  | `errkb doctor`                                                                      | `cmd/errkb/doctor.go`, `internal/service/doctor.go`                                                                                  | Implemented                                                                               |
| Apply and inspect migrations                      | `errkb db migrate`, `errkb db status`                                               | `cmd/errkb/db.go`, `internal/database/migrate.go`                                                                                    | Implemented in current source                                                             |
| Run a local toolbox window                        | `errkb ui`, `errkb ui --web`                                                        | `internal/ui/`, `internal/webshell/`, `cmd/errkb/web.go`                                                                             | Embedded Web shell shipped in v0.1.1 (`errkb ui --web`; the release includes it)          |
| Manage local PostgreSQL                           | PostgreSQL manager and portable scripts                                             | `internal/service/pg_manage.go`, `packaging/windows/postgres.ps1`                                                                    | Implemented; `errkb pg install` creates the cluster, `autostart.ps1` restores it at logon |
| Retain/delete data safely                         | Retention/deletion functions and settings models                                    | `0006_retention.sql`, `internal/ui/settings.go`                                                                                      | Implemented, including Web preview/confirm; automatic scheduling remains outside v1       |

## 4. Failure capture

### 4.1 Generic CLI wrapper

`errkb run -- <command>` starts a child process, forwards stdin, keeps stdout and stderr visible, and returns the child's exit code. A successful command is not recorded. A non-transient failure becomes `ErrorEvent v1`, is redacted and is sent to the daemon. After recording, the same event is retrieved so a known solution can be printed.

```text
cmd/errkb/run.go
  → internal/capture/run.go
  → redact + classify + headline + fingerprint
  → service.Client.Capture / Named Pipe
  → service.SQLBackend.Capture
  → database.RecordEvent + database.Retrieve
```

Repeated runs append occurrences to the same issue instead of creating unrelated issues.

### 4.2 Host adapters

The DSH plugin listens to host events and translates only fields the host exposes. The connector protocol offers capture, lookup, notice and feedback operations. A capability document states what is available and what is degraded; unsupported data is not guessed.

- DSH implementation: `src/plugin.ts`, `src/connector.ts`.
- Go connector contract and probes: `internal/capture/connector.go`.
- Host capability records: `connectors/*/capability.json`.
- Measured behavior: [connector-matrix.md](connector-matrix.md).

Codex and Claude can also expose MCP tools. MCP requires the model or user to call a tool; installing MCP alone does not watch failures.

### 4.3 Offline queues

When the daemon is unavailable, observations can be queued and replayed idempotently. The Go queue stores already-redacted events with count, byte and age limits (`internal/service/queue.go`). The TypeScript DSH adapter has a separate queue (`src/state.ts`); its raw-observation limitation is documented in [privacy.md](privacy.md).

## 5. Error processing

### 5.1 Classification

The classifier maps observable evidence into `tool`, `command-exit`, `llm` or `agent`. It also derives severity and whether a failure is transient. Transient failures can be counted without immediately becoming durable issues.

This is deterministic logic in `internal/core/classify.go` and `src/capture.ts`; it does not ask an LLM to decide.

### 5.2 Redaction

`spec/redact.cue` is the rule source. Generated Go and TypeScript rules replace secrets, credentials, e-mail addresses, request identifiers, home paths and other sensitive shapes before a database write. Contract validation rejects payloads that still look sensitive. A shared corpus tests Go, TypeScript and CI behavior.

### 5.3 Normalization and fingerprinting

Normalization removes unstable values such as ANSI escapes, GUIDs, timestamps, PIDs, counters, ports and absolute paths. The stable text is lower-cased and fingerprinted as:

```text
sha256(category + "\0" + normalized_message)[:12]
```

The implementation lives in `internal/core/normalize.go`, `fingerprint.go` and `src/signature.ts`. A fingerprint is an identity aid, not a claim that approximately similar text is identical.

## 6. Storage model

PostgreSQL is the system of record:

| Entity                                                 | Meaning                                                                                                                 |
| ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- |
| `connectors`, `sessions`                               | Where and during which host session a failure was observed                                                              |
| `events`                                               | Immutable, redacted failure observations                                                                                |
| `issues`                                               | Reusable problem identity for a project and fingerprint                                                                 |
| `occurrences`                                          | Every repeat of an issue, with environment and time                                                                     |
| `solutions`, `solution_versions`                       | Solution lifecycle and append-only content history                                                                      |
| `outcomes`                                             | Whether a solution version worked in an environment                                                                     |
| `retrievals`                                           | Query, result, match reason and duration                                                                                |
| `notices`                                              | Which known-fix notice was shown                                                                                        |
| `audit_log`                                            | Administrative and corrective changes                                                                                   |
| `prevention_artifacts`, `prevention_artifact_versions` | Unreleased prevention identity, repository-relative path, immutable content hash, lifecycle and Git activation evidence |
| `issue_preventions`, `prevention_verifications`        | Same-project issue links and append-only redacted command results                                                       |
| `schema_migrations`                                    | Applied migration versions and checksums                                                                                |

`internal/database/migrate.go` applies each SQL migration in a transaction under an advisory lock. The checksum ledger prevents an applied migration from being silently rewritten.

The original DSH Markdown store in `src/store.ts` remains for compatibility and fallback; it is not the target system of record. Import/export compatibility is implemented in `internal/database/markdown.go`.

## 7. Retrieval and fix reuse

Retrieval has four stages:

1. **Exact search:** issue ID, fingerprint, error code, normalized message, command or tool name (`0004_exact_search.sql`, `internal/database/search.go`).
2. **Candidate generation:** PostgreSQL `pg_trgm` GIN indexes produce a bounded set (`0005_trigram.sql`, `internal/database/candidates.go`).
3. **Reranking:** Go uses Jaccard token similarity, rejects contradictory structured fields, computes environment differences and keeps at most three (`internal/core/rerank.go`, `internal/database/rerank.go`).
4. **Presentation:** each result carries a reason and matched fields. Non-exact results carry `approximate: true` and require confirmation.

Approximate similarity can suggest a solution, but cannot silently merge issues or execute a fix. Performance evidence is in [benchmarks.md](benchmarks.md).

## 8. Solution lifecycle and trust

`solutions` stores lifecycle state; `solution_versions` stores append-only versions. MCP `err_record_solution` or the service API can record a verified fix. Corrective operations can set an issue to `open`, `resolved`, `wontfix` or `misjudged`.

Users and hosts report `success`, `failure` or `unknown` for a specific solution version. Trust is computed, not entered manually:

```text
trust = (successes + 2) / (successes + failures + 4)
```

Unknown results do not change the score. Environment-specific outcomes let the UI warn when the verified OS, shell, connector or runtime differs.

### 8.1 Prevention loop (Unreleased)

The four prevention kinds are `regression_test`, `deterministic_check`,
`agent_instruction` and `intent`. Registration requires a resolved issue and
creates or links immutable content-hash versions. A latest matching pass is
required before activation; activation also requires the artifact to be clean
and tracked at Git `HEAD`. Replacement deprecates the old active version without
erasing its history. Recurrence does not rewrite state automatically.

```text
cmd/errkb/prevention.go
  → service.HashPreventionArtifact (add / verify / activate)
  → service.Client / Named Pipe / API.Handle
  → service.SQLBackend.Prevention*
  → database.RegisterPrevention / RecordPreventionVerification /
    ActivatePrevention / ListPreventions / GetPrevention / PreventionStats
  → migration 0009 tables, constraints, indexes and audit_log
```

Verification uses `capture.Run` with empty stdin, a 120-second deadline and
bounded output; the artifact hash is checked before and after. Only CLI exposes
verify/activate. MCP `err_prevention_register/list/get` uses the same backend
but register accepts metadata and a caller-supplied hash without reading a file.
Web exposes only `GET /api/preventions/stats` through `client.PreventionStats`.
Metrics come from `database.PreventionStats` SQL aggregates over real
occurrences. See [prevention.md](prevention.md) for the complete workflow.

## 9. Interfaces

### 9.1 CLI

The CLI is a thin client over the same contracts: `search`, `show`, `record`, `list`, `feedback`, `stats`, `prevention`, `run`, `db`, `daemon`, `doctor`, `mcp`, `ui` and `version`. Human and `--json` output use the same validated response.

### 9.2 Daemon and local API

`errkb daemon` owns the PostgreSQL pool and serves framed JSON over `\\.\pipe\errkb`. The pipe has an owner-only ACL. The daemon enforces one instance per Windows session, rejects an unmigrated database, writes a health file, rotates redacted logs and drains in-flight work during shutdown.

### 9.3 MCP

`errkb mcp` provides twelve stdio tools in Unreleased source; v0.1.3 provides the first nine:

| Tool                      | Purpose                                                                                           |
| ------------------------- | ------------------------------------------------------------------------------------------------- |
| `err_lookup`              | Exact-oriented lookup by issue, code, fingerprint or normalized text                              |
| `err_search`              | Free-text search with explicit approximate flags                                                  |
| `err_get_issue`           | Read one issue with its occurrences, solutions and versions                                       |
| `err_record`              | Submit one already-redacted event                                                                 |
| `err_feedback`            | Record whether a solution worked                                                                  |
| `err_list`                | List issues or solutions                                                                          |
| `err_stats`               | Read totals, latency and saving estimates                                                         |
| `err_record_solution`     | Add and verify a fix                                                                              |
| `err_set_status`          | Correct an issue status                                                                           |
| `err_prevention_register` | Register prevention metadata and a caller-supplied content hash without reading the artifact file |
| `err_prevention_list`     | List prevention artifacts and their current lifecycle state                                       |
| `err_prevention_get`      | Read one prevention artifact, its versions, links and verification history                        |

MCP calls the daemon backend and revalidates responses. It neither connects directly to PostgreSQL nor passively monitors the host.

### 9.4 Native and Web UI

`internal/ui/` contains tested models for tray, omnibox, detail, solution editing, statistics and settings. `internal/webshell/` presents those capabilities through the embedded browser interface.

The local `errkb ui --web` embeds static assets, binds only to `127.0.0.1`, generates a random per-launch token and adds restrictive headers. Search, issue detail and editing, statistics, audit, settings, retention and deletion preview/confirmation are wired to the existing daemon services. Unreleased source also shows read-only Prevention statistics; prevention registration, verification, activation and issue linking remain CLI/MCP operations. See [toolbox-ui.md](toolbox-ui.md).

## 10. Operations, privacy and recovery

- `errkb doctor` checks configuration, database/schema state, privacy patterns, queue health and connector capability without changing them.
- `errkb db migrate/status` applies migrations or compares the ledger without starting the daemon. Unreleased source adds `0009_prevention.sql`; upgrade, rollback and future-ledger rejection are tested against real PostgreSQL.
- PostgreSQL lifecycle code can discover binaries, create/adopt a cluster, generate credentials, start/stop it, back it up, upgrade it and uninstall it (`internal/service/pg_manage.go`). Portable backup/restore includes all four prevention tables, their immutable versions, issue links and verification outcomes.
- Retention has separate sample, normalized-event and audit periods. Scoped deletion supports preview, confirmation and audit (`0006_retention.sql`), including prevention artifacts and cross-issue links; retention keeps active artifacts and the remaining occurrence evidence used by prevention metrics.
- Crash tests kill real writer and migration processes and verify committed-data integrity.

The Windows smoke job builds the release binary and runs the full suite with a real PostgreSQL. `release.ps1` produced and verified the portable `v0.1.3` bundle on 2026-10-25; the installer path was removed.

## 11. Explicit non-features

Version one does not:

- read or store a model's private chain of thought;
- reliably decide that a model misunderstood a requirement or hallucinated;
- watch every desktop app through screenshots or OCR;
- execute a retrieved fix automatically;
- generate regression tests, edit prevention artifact files or perform Git writes automatically;
- call a model or an external service to make prevention decisions;
- upload the knowledge base or provide team/cloud synchronization;
- use embeddings or vector search.

These boundaries keep v1 focused on observable, reproducible technical failures.

## 12. How to extend a feature safely

1. Add or reuse a CUE contract; do not invent interface-only fields.
2. Add a capability probe and mark unsupported host fields explicitly.
3. Translate input to `ErrorEvent v1` and redact before persistence or queueing.
4. Reuse daemon/backend operations; do not duplicate retrieval in an adapter.
5. Return match reason, approximate status, environment applicability and trust.
6. Add unit, contract/corpus and real-host acceptance tests.
7. Update both language versions, the connector matrix and project status.

This keeps one source of truth for error identity and prevents every model integration from becoming a separate product.
