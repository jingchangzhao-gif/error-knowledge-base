# Batch 3: CLI Registration, Explicit Verification, Activation, and Real Git E2E

Prerequisite: batch 2 acceptance is complete.  
Goal: deliver working `prevention add / verify / activate / list / show` commands and a real CLI loop. `prevention stats` arrives with metrics in batch 5.

## 1. Files in scope

Create:

- `cmd/errkb/prevention.go`
- `cmd/errkb/prevention_test.go`
- `tests/e2e/prevention_loop_test.go`

Modify:

- `cmd/errkb/main.go`
- `cmd/errkb/cli.go`
- `cmd/errkb/cli_test.go`
- `internal/service/client.go`
- `internal/service/client_test.go`
- `internal/service/prevention.go` (only if batch 2 has not exported the hashing entry required below)

Do not change MCP, Web, schema, or stats implementation. Never commit the current project repository in tests.

## 2. Pipe client and timeouts

- [ ] Write client tests first, then add `PreventionAdd`, `PreventionVerify`, `PreventionActivate`, `PreventionList`, and `PreventionGet` to `service.Client`. Each calls its operation, decodes, and validates.
- [ ] Extend `apiClient` in `cmd/errkb/cli.go` so production and test fakes share the same five-method surface.
- [ ] Keep the normal 30-second timeout. Add `callContextWithTimeout(timeout)` and use a 125-second pipe timeout for verify, leaving five seconds around the core's 120-second execution timeout.
- [ ] Do not lengthen add/list/show/activate timeouts or let a stuck daemon block a shell indefinitely.
- [ ] Cover response fields, backend errors, malformed JSON, wrong operation, and a valid failed-verification response.

## 3. CLI arguments and output

- [ ] Add `prevention` dispatch to `runCommand` and a usage summary. `runPrevention` returns `exitUsage=2` for missing or unknown subcommands.
- [ ] Keep global `--json` valid before or after commands. Human and JSON output come from the same contract response; do not invent out-of-contract business fields.

### 3.1 add

```powershell
errkb prevention add --issue <issue-id> --kind regression_test --title "Windows concurrent lock regression" --repo <repository-path> --path tests/windows_lock_regression_test.go
```

- [ ] Require all five flags and the closed four-kind enum.
- [ ] Call the core's exported `HashPreventionArtifact(repo, path)` for path/file safety and SHA-256. The CLI does not duplicate path rules or send bodies to the database.
- [ ] Send `PreventionAddRequest`; print artifact id, version id, draft state, and the next verify command. Duplicate registration displays `created=false`.

### 3.2 verify

```powershell
errkb prevention verify <version-id> --repo <repository-path> -- go test ./tests -run WindowsLock
```

- [ ] Reuse `splitRunArgs` at the first `--`. Before it, require exactly one version id and `--repo`; after it, require at least one non-empty command argument.
- [ ] Send argv unchanged. Never invoke a shell or join then reparse a command string.
- [ ] Print verification id, passed, exit code, duration, version state, and bounded redacted evidence. Suggest activate after a pass.
- [ ] A persisted `passed=false` returns `exitInternal=1`; bad arguments return 2; unavailable daemon returns 4. Failure never exits successfully.

### 3.3 activate

```powershell
errkb prevention activate <version-id> --repo <repository-path>
```

- [ ] Accept only a version id and required repo. Success prints active state, commit, activation time, and the latest version view reflecting old-version deprecation.
- [ ] Same-version/same-commit retry prints `activated=false` and exits 0. Hash changes, dirty paths, untracked files, and state conflicts return non-zero without changing the old active version.

### 3.4 list / show

- [ ] `list` supports `--issue`, `--project`, `--kind`, `--status`, `--limit`, and `--cursor`, with no positional arguments. Print artifact, latest version state, and next cursor.
- [ ] `show <artifact-id>` prints artifact, every version, latest verification, and activation commit. Missing artifacts return `exitNotFound=3`.
- [ ] Draft suggests verify and verified suggests activate. Instructions use a repository-path placeholder rather than guessing an absolute path from the database.

## 4. CLI unit tests

- [ ] With a fake `apiClient`, cover all five commands, argument parsing, `--json`, flags before/after positionals, unknown subcommands, and exit codes.
- [ ] Prove verify does not use a shell: argv containing spaces, quotes, and `;` reaches the fake element-for-element.
- [ ] Prove add sends only relative path and hash, never body. Files over 1 MiB, symlinks, and `../` fail before dialing.
- [ ] Failed verification prints evidence then returns 1; idempotent activation returns 0; empty list returns 0; missing show returns 3.
- [ ] Check existing usage and doctor/MCP selftests; do not advertise prevention stats early.

Focused commands:

```powershell
go test ./cmd/errkb -run 'Prevention|Usage' -count=1 -v
go test ./internal/service -run 'Client.*Prevention|Prevention' -count=1 -v
```

## 5. Real end-to-end acceptance

In `tests/e2e/prevention_loop_test.go`, reuse the private schema, real binary, Named Pipe, and daemon facilities from `cli_loop_test.go`. The Git repository must be under `t.TempDir()`.

- [ ] Write `TestCLIPreventionLoop` first. It may explicitly skip without `ERRKB_TEST_DATABASE_URL` or Git, but formal batch acceptance requires both and no SKIP.
- [ ] Record a failure and resolve its issue. Create a regression-test file in the temporary repository; the test fixture explicitly runs `git add`/`git commit`. This is fixture preparation, not product Git automation.
- [ ] Run compiled `errkb.exe prevention add`; assert draft and database metadata without body or absolute repository path.
- [ ] Run a real deterministic zero-exit verification through CLI. Assert verified state, verification row, redacted evidence, and audit.
- [ ] Activate through CLI; assert active state and the current 40-character HEAD. Repeat and assert idempotency with no additional audit.
- [ ] Modify the artifact without committing; activation must fail and old active state, commit, and audit must remain.
- [ ] Restore the clean file and record the same error again; assert a new occurrence linked to the original issue. Window metrics remain batch 5 scope.
- [ ] Run real non-zero and timeout helpers; failed records append and a latest failure blocks later activation.
- [ ] Track temporary HEAD/status throughout. Apart from explicit fixture preparation, product commands must not change HEAD, index, or working tree.

E2E command:

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 3 acceptance' }
go test ./tests/e2e -run 'TestCLIPreventionLoop' -count=1 -p 1 -v -timeout 10m
```

## 6. Completion and handoff

- [ ] Run `gofmt` on batch Go files, then `go test ./cmd/errkb ./internal/service ./tests/e2e -count=1 -p 1 -timeout 15m`.
- [ ] Run `go vet ./cmd/errkb ./internal/service ./tests/e2e` and `git diff --check`.
- [ ] Confirm only CLI exposes verify/activate, with no background verification, automatic activation, automatic file edits, or product-side Git writes.

Handoff to batch 4: five client methods and real CLI examples. MCP/Web may reuse add/list/get but must not forward verify/activate.
