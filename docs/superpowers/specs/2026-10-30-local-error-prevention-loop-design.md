# Local Error Prevention Loop v1 Design Specification

Date: 2026-10-30  
Status: awaiting project-owner approval  
Target baseline: `main` after `error-knowledge-base` v0.1.3

## 1. Decision

The next stage will not turn the project into an autonomous repair agent or a team cloud service. The first implementation delivers one verifiable local vertical loop:

```text
resolved issue
  → register a prevention artifact in a repository
  → explicitly run a verification command as the user
  → explicitly activate the verified version
  → link the Git commit
  → observe real recurrences after activation
```

The product expands from “repair a repeated error quickly” to “repair it and prevent its class,” without changing the existing capture, retrieval, solution, trust, or privacy boundaries.

## 2. Goals and non-goals

### 2.1 Required goals

1. A user can link a resolved issue to a regression test, deterministic check, agent instruction, or `intent.md`.
2. Artifact content lives only in the user-selected Git repository. PostgreSQL stores metadata, links, verification, and audit evidence, never a second authoritative copy.
3. Every verification binds to an immutable artifact version and SHA-256. Content changed after verification cannot be activated directly.
4. Only an explicit user CLI `verify` or `activate` action can change verification or activation state.
5. An agent may register drafts and read state through MCP, but cannot verify, activate, or execute commands through MCP.
6. The toolbox shows which errors have prevention, whether it is verified, and whether recurrence continues after activation.
7. The flow calls no model, uses no external network, writes no project files automatically, and performs no Git mutations.

### 2.2 Explicit non-goals

- Generating regression tests, hooks, rules, skills, or fixes automatically.
- Editing `CLAUDE.md`, `AGENTS.md`, tests, or project configuration automatically.
- Running `git add`, `git commit`, creating PRs, or deploying automatically.
- Production monitoring, control bands, autonomous diagnosis, and autonomous PRs.
- Team permissions, cloud sync, multiple users, and cross-machine sharing.
- Vector retrieval, embeddings, or a model inside the Go core.
- Claiming a precise number of prevented errors because none was observed.

These require separate later specifications and must not block this vertical slice.

## 3. Domain model

### 3.1 PreventionArtifact

A durable prevention intent, such as “prevent the Windows lock race from breaking concurrent writes again.” It belongs to one `project_key` and may link to multiple issues.

Kinds:

- `regression_test`: a code regression test or agent eval.
- `deterministic_check`: a doctor, lint, hook, or CI check.
- `agent_instruction`: a short advisory rule for an agent.
- `intent`: an `intent.md` requiring further design or broader work.

### 3.2 PreventionArtifactVersion

One immutable content version containing a repository-relative path and content SHA-256. Content is not stored in PostgreSQL. Changed content requires a new version.

Lifecycle:

```text
draft → verified → active → deprecated
  └──────────────────────→ deprecated
```

Rules:

- New versions start as draft.
- Only successful verification of the same version and SHA-256 makes a version verified.
- An artifact has at most one active version.
- Activating a new version deprecates the previous active version in one transaction.
- Adding a draft does not disturb the active version.
- Deprecated versions cannot be restored; create a new version instead.

### 3.3 Verification versus activation

- **Verification** is mechanical evidence: an explicitly supplied user command exited zero against this version.
- **Activation** is a human decision: the user confirms this version is in the repository HEAD and includes it in recurrence measurement.
- Verification never implies activation, and an agent cannot activate its own proposal.

## 4. User flow

### 4.1 Register

Precondition: the issue exists and is resolved.

```powershell
errkb prevention add `
  --issue <issue-id> `
  --kind regression_test `
  --title "Windows concurrent lock regression" `
  --repo <repository-path> `
  --path tests/windows_lock_regression_test.go
```

The system validates the issue and input; rejects absolute paths, `..`, `.git`, and empty paths; and reads at most 1 MiB and computes SHA-256 without storing content or the repository absolute path. It locates an existing artifact by `(issue_id, kind, artifact_path)`, creating the artifact and issue link only when none exists. Registering the same `artifact_path + content_sha256` returns the existing draft/verified version without duplicate audit; a different hash appends the next draft version. New artifacts, links, and versions are audited; idempotent reads are not.

### 4.2 Verify

```powershell
errkb prevention verify <version-id> --repo <repository-path> -- go test ./tests -run WindowsLock
```

The version must be draft or verified. The artifact hash must match registration. The command starts directly without an implicit shell and has a 120-second default timeout. Exit code, duration, environment summary, and bounded redacted evidence are appended to history. The artifact hash must be unchanged after the run. Exit zero records success and marks the version verified; failure or timeout preserves its current state. Every run is appended. A verified version may be verified again, but a failed latest run blocks activation. Active or deprecated versions cannot be verified again.

### 4.3 Activate

```powershell
errkb prevention activate <version-id> --repo <repository-path>
```

Activation requires a verified version whose latest verification passed against the current hash. It recomputes the hash and uses read-only Git commands to require the path in HEAD, no uncommitted change at that path, and the current 40-character HEAD SHA. It never runs checkout, add, commit, push, or another Git mutation. One transaction deprecates the old active version, activates the new one, stores commit evidence, and writes audit. Repeating activation of the same version at the same commit returns the existing state without a duplicate audit entry.

### 4.4 Observe

The issue detail and statistics pages show linked artifacts, kind, version, state, latest verification, activation commit, pre/post 30-day occurrence windows, and recurrence after activation. A new occurrence does not automatically disable prevention or reopen the issue.

## 5. Architecture and ownership

```text
CLI / MCP / Web toolbox
          │
          ▼
versioned prevention contracts
          │
          ▼
local daemon / Named Pipe
          │
          ▼
prevention application service
          │
          ├── PostgreSQL: metadata, links, runs, activation, audit
          └── local filesystem + read-only Git: hash and commit evidence
```

- Go owns validation, lifecycle, hashing, explicit verification, and metrics.
- PostgreSQL owns referential integrity, the single-active-version constraint, transitions, and audit transactions.
- CUE defines the new contract and joins the existing compatibility gate.
- CLI is the only verification and activation entry point.
- MCP provides draft registration and read-only queries only.
- Web is read-only and shows CLI guidance; it never executes commands.
- ErrorEvent v1 remains unchanged. Prevention uses its own v1 contract.

## 6. PostgreSQL design

Add `0009_prevention.sql`; never edit published migrations 0001–0008.

### 6.1 `prevention_artifacts`

`id uuid primary key`, `project_key`, `kind`, `title`, `created_at`, `updated_at`, `source`, and `actor`. Kind is restricted to the four defined values.

### 6.2 `prevention_artifact_versions`

`id`, `artifact_id` with cascade delete, `version`, `artifact_path`, 64-character lowercase `content_sha256`, `status`, nullable 40-character `commit_sha`, nullable `activated_at`, `created_at`, `source`, and `actor`. Enforce `unique (artifact_id, version)` and a partial unique index allowing at most one active version per artifact. Content identity fields are immutable; lifecycle fields change only through service transactions.

### 6.3 `issue_preventions`

`issue_id` and `artifact_id`, both cascading on delete, plus `created_at`, `source`, and `actor`. The pair is the primary key.

### 6.4 `prevention_verifications`

`id`, `artifact_version_id` with cascade delete, redacted `command`, nullable `exit_code`, `passed`, `duration_ms`, bounded redacted `evidence`, `environment`, `content_sha256`, `created_at`, `source`, and `actor`.

### 6.5 Operational compatibility

Scoped deletion preview and execution must include the new tables. Retention must not silently remove active artifact metadata. Backup/restore tests must cover all new records. Business deletion must not cascade into `audit_log`. Index issue links, artifact versions, activation time, and verification records so recurrence statistics do not sequentially scan all occurrences.

## 7. Contracts and APIs

Add `spec/prevention.cue` at v1 and register it in `spec/compat.cue`.

All payloads carry `schema_version`, reject unknown fields by default, redact free text before persistence, use repository-relative slash paths, and never store repository absolute paths. Do not add `auto_fix`, `approved_by_agent`, `prevented_count`, or a directly writable trust field.

Local API operations:

- `prevention_add`
- `prevention_verify`
- `prevention_activate`
- `prevention_list`
- `prevention_get`
- `prevention_stats`

Verification and activation are wired only into the explicit local CLI workflow, not MCP or Web. Human approval is a product-workflow constraint, not cryptographic authentication between local processes; agent activation is prevented by keeping these operations out of the MCP tool surface.

## 8. Interfaces

### 8.1 CLI

```text
errkb prevention add
errkb prevention verify
errkb prevention activate
errkb prevention list
errkb prevention show
errkb prevention stats
```

Writes return artifact/version IDs, state, and the next command. `--json` returns the validated contract. Verification failure exits non-zero while preserving the failed run. Activation failure leaves the old active version untouched. Process capture reuses existing bounds, timeout, environment allowlist, and redaction behavior.

### 8.2 MCP

Only three tools are added:

- `err_prevention_register`: creates a draft from issue, kind, title, relative path, and caller-computed SHA-256.
- `err_prevention_list`: filters by issue, project, kind, or status.
- `err_prevention_get`: reads version, verification, and activation history.

MCP exposes no verification, activation, command execution, file writes, or Git mutations. An agent may suggest a next step but cannot claim it verified or activated anything.

### 8.3 Web toolbox

The first version is read-only. Issue detail gains a Preventions section, statistics gains a Prevention section, drafts show verify CLI guidance, and verified versions show activate CLI guidance. There is no web command box or browser credential collection.

## 9. Metrics

Metrics must distinguish facts from interpretation. Never display an unsupported “N errors prevented.”

Required metrics:

1. Artifact counts by state and kind.
2. Verification pass rate: passed runs divided by all runs.
3. Time from issue `first_seen` to the first linked artifact `activated_at`.
4. Recurrences after activation: linked issue occurrences after `activated_at`.
5. Pre/post 30-day windows are calculated per activation of an `(issue_id, artifact_version_id)` pair: `[T-30d, T)` and `(T, T+30d]`.
6. Pre-window valid days are `min(30, T-first_seen)` and post-window valid days are `min(30, now-T)`, measured in whole days; occurrence counts and valid days are shown together.
7. Mature protection rate: among activation records with a complete 30-day post-window, the share with no recurrence in that window.
8. Active artifacts with recurrence, so the user can add a version, deprecate, or create an intent.

A post-window shorter than 30 days displays “observing” and enters neither the numerator nor denominator. No reduction percentage is shown for incomplete windows. Repeated activations of one issue remain separate activation records rather than an unexplained aggregate.

## 10. Security, privacy, and failure behavior

Verification is explicit only; no scheduler or MCP route exists. Commands use no implicit shell, and artifact text is never interpreted as a command. Timeout defaults to 120 seconds. Output is bounded and redacted; repository absolute paths are not persisted. Files are limited to 1 MiB; symlinks, device files, and paths escaping the repository are rejected. Hash drift, a dirty Git path, a path absent from HEAD, and illegal transitions fail explicitly. Database failure never causes compensating file or Git writes. Activation rollback preserves the old active version. Agent or host failure does not impair the CLI loop, and prevention failure does not interrupt existing capture or retrieval.

## 11. Testing and acceptance

### 11.1 Automated coverage

Cover CUE valid/invalid payloads, unknown fields, paths and hashes; schema 0008→0009 upgrade, rollback and old-data preservation; lifecycle, single-active-version, concurrent activation and old-version deprecation; idempotency and audit; hash-drift rejection; verification timeout, non-zero exit, truncation and redaction; untracked, dirty and non-Git rejection; absence of MCP verify/activate; scoped deletion, backup/restore and audit; metric boundaries; and all existing Go, TypeScript, CUE, compatibility and privacy gates.

### 11.2 End-to-end acceptance

Using a real temporary Git repository and PostgreSQL:

1. Record a failure and resolve its issue.
2. Create and commit a regression test.
3. Register a draft through CLI.
4. Run a real passing verification command.
5. Activate and confirm the current HEAD is recorded.
6. Modify without committing and confirm reactivation is rejected.
7. Record the same error again and confirm one post-activation recurrence is shown.
8. Confirm MCP publishes no verify or activate tool.
9. Demonstrate no model call, external network, or automatic Git mutation.

Completion requires the whole user flow to run, not merely tables and commands to exist. Update both languages of README, feature implementation, project status, privacy documentation, and changelog.

## 12. Implementation order

1. Contracts, compatibility rules, and migration 0009.
2. Go service, lifecycle, audit, and database tests.
3. CLI registration, verification, activation, and real-Git end-to-end flow.
4. MCP draft/read APIs and read-only Web presentation.
5. Metrics, operational compatibility, bilingual documentation, and release acceptance.

Each step must be independently verifiable. Automatic generation and anomaly monitoring are not parallel workstreams for this slice.

## 13. Later independent versions

After acceptance, separately review:

1. Agent-generated regression or check drafts, still verified and activated by the user.
2. Short agent-instruction candidates backed by eval evidence.
3. Deterministic hook/CI exports for mandatory policies.
4. Deterministic control bands that create triage intents only.
5. Restricted automatic PRs as the last candidate, always with human review.

The product remains a local error knowledge base rather than expanding unchecked into a complete SDLC platform.
