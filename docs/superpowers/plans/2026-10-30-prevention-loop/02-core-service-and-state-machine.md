# Batch 2: Go Prevention Service, State Machine, Audit, and Database Tests

Prerequisite: batch 1 acceptance is complete.  
Goal: implement the core and local API backend for add/verify/activate/list/get without CLI, MCP, Web, or a stats command.

## 1. Files in scope

Create:

- `internal/database/prevention.go`
- `internal/database/prevention_test.go`
- `internal/service/prevention.go`
- `internal/service/prevention_test.go`
- `internal/service/prevention_api_test.go`

Modify:

- `internal/service/api.go`
- `internal/service/backend.go`
- `internal/service/pipe_test.go` and other existing test fakes affected by the `Backend` extension
- `internal/capture/run.go`
- `internal/capture/run_test.go`

Do not change CLI, MCP, Web, or statistics structures. Do not call models or add dependencies.

## 2. Data access and registration transactions

- [ ] Write failing database tests first, then define `database.PreventionArtifact`, `PreventionVersion`, `PreventionVerification`, `PreventionFilter`, `PreventionRegistration`, `PreventionRegistrationResult`, and `PreventionVerificationInput`.
- [ ] Implement `RegisterPrevention`, `GetPreventionVersion`, `RecordPreventionVerification`, `ActivatePrevention`, `ListPreventions`, and `GetPrevention` from the master plan. Leave `PreventionStats` to batch 5; do not add an empty stub.
- [ ] `RegisterPrevention` locks an advisory transaction key for `(issue_id, kind, artifact_path)` and requires an existing `resolved` issue whose project matches the artifact.
- [ ] Same path/hash returns the latest existing version with `created=false` and no new business or audit rows. A different hash appends `max(version)+1` as draft with `created=true`.
- [ ] New artifacts, issue links, and versions write `prevention.artifact.create`, `prevention.issue.link`, and `prevention.version.create` respectively. A transaction failure leaves no partial registration.
- [ ] Redact title, source, and actor with the existing engine before writing. The database receives neither body nor `repo_path`.
- [ ] List uses stable `(updated_at, artifact_id)` cursor pagination and issue/project/kind/status filters. Detail sorts versions by number and verifications by time. Empty results are non-nil empty arrays.

Registration tests:

- [ ] Reject open/wontfix/misjudged issues; accept resolved issues.
- [ ] Duplicate registration is idempotent with unchanged audit count; a different hash appends only a draft and leaves the active version untouched.
- [ ] Two concurrent registrations of one key create one artifact, one link, and one version.
- [ ] An artifact can link multiple issues; cross-project links and missing issues are rejected.

## 3. Verification state machine

- [ ] `RecordPreventionVerification` locks the version and artifact and accepts only draft/verified; active/deprecated returns conflict.
- [ ] Every run appends one `prevention_verifications` row and one `prevention.verify` audit row. Never overwrite history or make verification runs idempotent.
- [ ] Passed moves the version to verified. Failure retains its previous draft/verified state. A verified version can have a latest failed run, which blocks activation.
- [ ] Verification `content_sha256` must equal the registered hash. `command` and `evidence` are bounded and redacted before storage; `environment` contains only the whitelisted summary.
- [ ] Non-zero exit, start failure, timeout, and changed pre/post artifact hash all produce failed records. A pre-run hash mismatch rejects before command execution and writes no verification.
- [ ] Test draft→verified→active→deprecated, plus rejection of active/deprecated re-verification, deprecated revival, and direct draft activation.
- [ ] Test verified→latest failed→activation rejection, then a new pass allowing activation.
- [ ] Test that run counts, latest result, exit codes, and audit entries append on every run.

## 4. Activation transactions

- [ ] `ActivatePrevention` locks the artifact and target version in one transaction and requires verified state, latest verification passed, and matching latest/registered SHA.
- [ ] Retrying an active version at the same commit returns it with `activated=false` and no audit. The same active version at a different commit returns conflict.
- [ ] Activating a new version deprecates the old active version in the same transaction, writes active state, 40-character commit, and activation time, then appends `prevention.activate` audit.
- [ ] Any validation or audit failure rolls everything back and preserves the old active version. Deprecated versions cannot be activated again.
- [ ] Concurrent activation of two versions leaves at most one active. The loser receives conflict or a deterministic fully retried state; no double-active or partial audit is possible.

## 5. Files, commands, and read-only Git

- [ ] Implement `resolvePreventionFile` in `internal/service/prevention.go`: canonicalize repository root and slash-relative path, rejecting escapes, `.git`, symlinks, non-regular files, and files over 1 MiB. Export `HashPreventionArtifact(repo, path)` so batch 3 CLI add reuses exactly this validation and hashing.
- [ ] Core add registers only the caller-supplied hash. Core verify/activate rereads `repo_path + artifact_path` and computes SHA-256; it never trusts another request hash.
- [ ] Verify uses `capture.Run` with repository root as `Dir`, `Recorder=nil`, and direct argv execution without `cmd /c`, PowerShell, or shell concatenation.
- [ ] Default verification timeout is 120 seconds. Reuse the existing 64 KiB head/tail capture for each stdout/stderr stream. Process final evidence with `redact.Sample`, including truncation, timeout, and start-failure notes.
- [ ] Export `capture.summarizeEnvironment` as `capture.SummarizeEnvironment` and reuse it. Persist only OS, architecture, shell basename, and Go version. The child may inherit its execution environment, but environment variables are never dumped.
- [ ] Hash the artifact again after the command. A changed artifact fails verification even when the command exits 0. The service does not stream child output through the CLI; the CLI later displays bounded redacted evidence.
- [ ] Activate invokes only read-only Git: resolve root and 40-character HEAD, prove the path exists in HEAD with `git cat-file -e HEAD:<path>`, and prove it clean with `git status --porcelain=v1 --untracked-files=all -- <path>`.
- [ ] Every Git call uses argv and bounded output. Reject non-repositories, untracked paths, staged-but-uncommitted files, working-tree modifications, and invalid HEAD. Never run checkout/add/commit/push/config or other writes.

Execution tests:

- [ ] Use helper processes from the current test binary for exit 0, non-zero, large output, secrets, sleep, and artifact modification; cover timeout and redaction.
- [ ] Use real Git repositories under `t.TempDir()` for tracked-clean, untracked, staged, modified, non-repository, and path-escape cases. Test identity configuration affects only the temporary repository.
- [ ] Compare HEAD, index, and working tree before/after activation to prove the core performs no Git write.

## 6. Local API backend

- [ ] Extend `service.Backend` and `API.Handle` only for `prevention_add`, `prevention_verify`, `prevention_activate`, `prevention_list`, and `prevention_get`; stats is wired in batch 5.
- [ ] Implement these five methods on `SQLBackend`: validate, call the core through `withConn` or a pool transaction, map to the batch 1 contract, and validate again.
- [ ] CLI and MCP share add/list/get, with no state machine in transport. MCP is not exposed yet, so verify/activate still has no user entry point in this batch.
- [ ] Map errors consistently in `translateBackendError`: missing→`not_found`, state/hash/Git conflict→`conflict`, structural validation→`invalid_request`, connection failure→`unavailable`, unknown→`internal`.
- [ ] Failed verification is a normal `PreventionVerifyResponse`, not a pipe error. Start failure and timeout are persisted as failed before returning that response.
- [ ] Test response mapping so `repo_path`, bodies, raw environment variables, and unredacted output never appear in responses.

## 7. Acceptance and handoff

Focused tests:

```powershell
go test ./internal/capture -run 'TestRun|Environment' -count=1 -v
go test ./internal/service -run 'Prevention|Pipe' -count=1 -v
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 2 acceptance' }
go test ./internal/database -run 'Prevention' -count=1 -p 1 -v
go test ./internal/service -run 'Prevention' -count=1 -p 1 -v
```

Regression checks:

```powershell
gofmt -w internal/database/prevention.go internal/database/prevention_test.go internal/service/prevention.go internal/service/prevention_test.go internal/service/prevention_api_test.go internal/service/api.go internal/service/backend.go internal/capture/run.go internal/capture/run_test.go
go test ./internal/contract ./internal/capture ./internal/service ./internal/database -count=1 -p 1
go vet ./internal/...
git diff --check
```

Expected: required PostgreSQL/Git tests do not SKIP; failure paths preserve the old active version; no CLI/MCP/Web verify or activate entry exists.

Handoff to batch 3: five callable `SQLBackend` methods, stable error codes, real file/command/Git capabilities, and database fixtures.
