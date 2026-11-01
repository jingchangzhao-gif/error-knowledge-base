# Prevention loop

[简体中文](prevention.zh-CN.md)

> **Source status: Unreleased.** The prevention loop is implemented and tested in the current source tree. It is not part of the released v0.1.3 toolbox.

The prevention loop turns a resolved error into a versioned, reviewable prevention artifact, proves that artifact with a real command, binds activation to a clean Git commit, and then measures later occurrences. It extends the existing capture → retrieval → outcome loop without calling a model or changing repository files.

Prevention metadata describes intent and evidence; activation does **not** automatically run a check on every build, inject an instruction into a host, or execute a retrieved fix. Wire the artifact into your own test, CI or host workflow explicitly.

## Artifact types

| Kind                  | Intended artifact                                              | Typical verification                                                       |
| --------------------- | -------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `regression_test`     | A test that reproduces the original failure and now passes     | Run the focused test or package suite                                      |
| `deterministic_check` | A lint rule, script, schema check or other deterministic guard | Run the check with a zero/non-zero result                                  |
| `agent_instruction`   | A reviewed instruction or checklist kept in the repository     | Run a deterministic validator; activation alone does not inject it         |
| `intent`              | A reviewed statement of the behavior that must remain true     | Attach an explicit verification command; do not treat prose alone as proof |

Each artifact has one project, a title, a repository-relative path and one or more linked issues in that same project. A changed content hash appends a new immutable version. Artifact bodies stay in the repository; PostgreSQL stores metadata, hashes, transitions, verification results and audit rows.

## State and authority

```text
resolved issue + local file
        │ prevention add
        ▼
      draft ── passing verification ──▶ verified
        │                                  │
        │                         latest run passes + clean,
        │                         tracked artifact at Git HEAD
        │                                  │
        └──────── failed runs are recorded, never activate
                                           ▼
                                         active
                                           │
                           activating a replacement version
                                           ▼
                                      deprecated
```

A failed verification remains useful evidence. Activation requires the latest verification to pass against the same hash. A new occurrence never automatically deprecates a version or changes an issue status.

Only the human-invoked CLI can execute verification or activate a version. MCP can register metadata and read records; the Web statistics area is read-only. The core makes no model calls, performs no automatic test generation or file edits, and performs no Git writes.

## Prerequisites

- Run the Unreleased source build with PostgreSQL migrated through `0009` and the daemon available.
- Start with a **resolved** issue ID.
- Keep the artifact at a repository-relative slash path such as `tests/rename_test.go`. Absolute paths, backslashes, `.`/`..`, `.git`, symbolic links and non-regular files are rejected.
- The file may not exceed **1 MiB**.
- Activation requires Git, a valid `HEAD`, the artifact tracked at that commit, and no uncommitted change to that artifact. All Git commands used by errkb are read-only.

## Six CLI commands

| Command                     | Purpose                                                          |
| --------------------------- | ---------------------------------------------------------------- |
| `errkb prevention add`      | Hash a local artifact and register a draft/version or issue link |
| `errkb prevention verify`   | Execute one bounded command and append its real result           |
| `errkb prevention activate` | Recheck hash and Git state, then activate the verified version   |
| `errkb prevention list`     | Page/filter artifacts by issue, project, kind or status          |
| `errkb prevention show`     | Read an artifact, all versions and verification history          |
| `errkb prevention stats`    | Read observed recurrence and verification metrics                |

All commands support the CLI's global `--json` mode. `verify` returns a non-zero CLI status after recording a failed run, so automation cannot mistake a persisted failure for success.

## Complete example

Assume the issue is already resolved and `tests/rename_test.go` is a real regression test in the current repository.

```powershell
# 1. Register the current bytes; no artifact body is sent to PostgreSQL.
errkb prevention add --issue <resolved-issue-id> --kind regression_test --title "Rename regression" --repo . --path tests/rename_test.go

# 2. Use the artifact_version_id returned by add. The command runs in --repo.
errkb prevention verify <artifact-version-id> --repo . -- go test ./...

# 3. Commit the exact tested artifact yourself. Activation only reads Git state.
git add tests/rename_test.go
git commit -m "Add rename regression test"
errkb prevention activate <artifact-version-id> --repo .

# 4. Inspect the state and evidence.
errkb prevention list --project <project-key> --status active
errkb prevention show <artifact-id>
errkb prevention stats --project <project-key>
errkb prevention stats --issue <resolved-issue-id> --json
```

To revise an artifact, edit it and run `prevention add` again with the same issue, kind and path. A changed SHA-256 creates a new draft. Verify and activate that version explicitly; the previous active version becomes deprecated while retaining its history.

## MCP boundary

The Unreleased MCP server has twelve tools: the existing nine plus:

- `err_prevention_register`: accepts metadata and a caller-supplied SHA-256. It does **not** read the file and does **not** prove that the hash matches repository content.
- `err_prevention_list`: reads filtered artifact/version pages.
- `err_prevention_get`: reads one artifact, its versions and verification history.

MCP deliberately exposes no verify or activate tool. Use the CLI for those decisions. The Web toolbox exposes a read-only Prevention statistics area and no prevention write route.

## Metrics: observations, not claims

`prevention stats` accepts optional `--project` and `--issue` filters. Counts are calculated in SQL from current database rows.

- `by_kind` counts distinct artifacts; `by_status` counts distinct versions. Multiple issue links do not inflate artifact totals.
- Verification pass rate is `passed runs / all runs`. With zero runs, both counts and the rate are zero.
- Every activated version produces one observation per linked issue. `first_seen_to_activation_ms` is clamped at zero.
- For activation time `T`, the before window is `[T-30*24h, T)` and the after window is `(T, T+30*24h]`. An occurrence exactly at `T` is in neither window.
- `before_days` and `after_days` are complete 24-hour days, clamped to `0..30`. Partial windows show their effective days and counts; no decline percentage is inferred.
- `recurrence_count` counts **all** occurrences after `T`, including those beyond the 30-day after window. It is not interchangeable with `after_count`.
- `after_days < 30` is `observing`; at 30 days it is `mature`. Only mature observations enter `mature_protection_rate`, which is the proportion whose `after_count` is zero. With no mature observations the rate is `null` / “no mature records”.
- `recurring_version_ids` contains only currently active versions with recurrence. There is deliberately no `prevented_count`: an absent error cannot be attributed to an artifact as a measured prevention.

Retention and scoped deletion affect subsequent metrics because they operate on the remaining real occurrences. Connector-only, time-only and retention operations do not silently delete active prevention artifacts; project deletion removes that project's prevention chain after preview and confirmation. Backups include all four prevention tables.

## Troubleshooting

| Symptom                                                | What to check                                                                                                       |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| Registration says the issue is not resolved            | Resolve or correct the issue first; prevention registration starts only from a resolved issue                       |
| Registration rejects the path                          | Use a regular file with a slash-separated repository-relative path; remove symlinks and keep it at or below 1 MiB   |
| Verification reports changed content                   | The file no longer matches the registered hash; register the changed bytes as a new version, then verify it         |
| Verification fails or exits non-zero                   | Read the redacted bounded evidence. Non-zero exit, start failure and 120-second timeout are recorded as failed runs |
| Activation says the latest verification is not passing | Re-run verification successfully against the exact current hash; an older pass is insufficient                      |
| Activation rejects Git state                           | Ensure `HEAD` exists, the exact artifact is tracked at `HEAD`, and `git status -- <path>` is clean                  |
| The daemon rejects the operation                       | Migrate to `0009`, restart the daemon, and use a build that includes the Unreleased prevention contracts            |
| Mature protection is empty                             | No activation has a full 30-day after window yet; this is not a 0% or 100% result                                   |
| Recurrence and after-window counts differ              | Recurrence is all-time after activation; `after_count` is only the 30-day window                                    |

## Privacy and operational limits

Artifact bodies and repository absolute paths are not stored in PostgreSQL. Verification command arguments and bounded stdout/stderr evidence are redacted before storage. Environment metadata uses a fixed allowlist rather than dumping process environment variables. Dumps still contain all stored prevention metadata and verification evidence, so protect them like the rest of the database.

See [privacy.md](privacy.md) for the complete data boundary and [features-and-implementation.md](features-and-implementation.md) for the source map.
