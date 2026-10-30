# Batch 5: Recurrence Metrics, Operations Compatibility, Bilingual Docs, and Release Acceptance

Prerequisite: batch 4 acceptance is complete.  
Goal: deliver `prevention stats`, the stats page, full data lifecycle, and final acceptance. Update Unreleased only; do not tag or publish.

## 1. Files in scope

Core and interfaces:

- `internal/database/prevention.go`
- `internal/database/prevention_test.go`
- `internal/service/prevention.go`
- `internal/service/prevention_test.go`
- `internal/service/api.go`
- `internal/service/backend.go`
- `internal/service/client.go`
- `internal/service/client_test.go`
- `cmd/errkb/prevention.go`
- `cmd/errkb/prevention_test.go`
- `tests/e2e/prevention_loop_test.go`

Web and operations:

- `internal/webshell/server.go`
- `internal/webshell/stats.go`
- `internal/webshell/stats_test.go`
- `internal/webshell/prevention.go`
- `internal/webshell/prevention_test.go`
- `internal/webshell/assets/app.js`
- `cmd/errkb/web.go`
- `internal/database/backup.go`
- `internal/database/backup_test.go`
- `internal/ui/settings.go`
- `internal/ui/settings_test.go`
- `spec/compat.cue` (upgrade guarantees and matrix only)
- `db/migrations/README.md`

Bilingual documentation:

- `README.md`, `README.zh-CN.md`
- `docs/features-and-implementation.md`, `docs/features-and-implementation.zh-CN.md`
- `docs/project-status.md`, `docs/project-status.zh-CN.md`
- `docs/privacy.md`, `docs/privacy.zh-CN.md`
- New `docs/prevention.md`, `docs/prevention.zh-CN.md`
- `CHANGELOG.md`

Do not edit migrations 0001–0009, bump the version, or claim a release.

## 2. Metric calculations

- [ ] Write fixed-time tests first, then implement `database.PreventionStatsAt(ctx, conn, filter, now)`. Add `PreventionStats` using current UTC; wire requests cannot supply a forged `as_of`.
- [ ] `by_kind` counts distinct artifacts and `by_status` counts distinct artifact versions. Multi-issue links cannot inflate totals.
- [ ] Verification pass rate is `passed runs / all runs`, with numerator and denominator returned. With zero runs, rate is 0 and runs=0.
- [ ] Every version with `activated_at` produces one `PreventionActivationStats` per linked issue; `first_seen_to_activation_ms=max(0,T-issue.first_seen)`.
- [ ] Before window is `[T-30d,T)` and after window is `(T,T+30d]`. Test both boundaries; an occurrence exactly at T belongs to neither.
- [ ] `before_days=clamp(floor((T-first_seen)/24h),0,30)` and `after_days=clamp(floor((now-T)/24h),0,30)`. A partial 24-hour day counts as 0.
- [ ] `before_count`/`after_count` cover only their 30-day windows. `recurrence_count` covers every occurrence after T. Never mix these counters.
- [ ] `after_days<30` is `observing` and excluded from mature protection; 30 days is `mature`. Mature protection is the share of mature records with `after_count=0`; with zero mature records the rate is null.
- [ ] Partial before/after windows display counts and effective days only, never a decline percentage. No response or UI contains `prevented_count`.
- [ ] `recurring_version_ids` includes only currently active versions with `recurrence_count>0`. A new occurrence does not deprecate anything or change issue state.
- [ ] Project/issue filters apply to counts, verification, and activation records. Queries use batch 1 occurrence/activation indexes rather than loading whole tables into Go.

Fixed-time cases:

- [ ] First seen less than 30 days before activation, post-activation under/exactly/over 30 days.
- [ ] Occurrences at T-30d, T, and T+30d, plus repeated occurrences of one issue.
- [ ] One artifact linked to two issues produces two activation observations but one artifact count.
- [ ] Multiple versions of one issue are separate; deprecated retains history while active alone enters the recurring list.
- [ ] Mixed mature-zero, mature-recurring, and observing records produce correct numerator, denominator, and nullable rate.

Database acceptance:

```powershell
if (-not $env:ERRKB_TEST_DATABASE_URL) { throw 'ERRKB_TEST_DATABASE_URL is required for batch 5 acceptance' }
go test ./internal/database -run 'PreventionStats|Prevention' -count=1 -p 1 -v
```

## 3. Stats API, CLI, and Web

- [ ] Extend `service.Backend`, `API.Handle`, `SQLBackend`, and `service.Client` for the already-defined `prevention_stats`; keep all other operations unchanged.
- [ ] Add `errkb prevention stats [--project <key>] [--issue <id>]`, printing state/kind counts, verification numerator/denominator, activation observations, mature protection, and recurring versions. `--json` returns the contract unchanged.
- [ ] Add Web `PreventionStatsFunc`, `EnablePreventionStats`, and `GET /api/preventions/stats`, delegated through `client.PreventionStats`. Add no prevention POST route.
- [ ] Add a Prevention section to the stats page: counts, pass rate, per-activation effective days/window counts/observing or mature state, mature protection, and recurring list.
- [ ] Null mature protection displays “no mature records”; observing displays “observing”, never 0% or 100% as a substitute conclusion.
- [ ] Escape all text and keep CLI instructions read-only. Existing core metrics, audit, and settings behavior remains unchanged.
- [ ] Extend real CLI E2E: after recording the same error post-activation, `prevention stats --issue` reports recurrence_count=1 and an observing record excluded from mature protection.

Commands:

```powershell
go test ./internal/service ./cmd/errkb ./internal/webshell -run 'Prevention|Stats' -count=1 -p 1 -v
go test ./tests/e2e -run 'TestCLIPreventionLoop|TestToolboxStatistics' -count=1 -p 1 -v -timeout 15m
```

## 4. Backup, restore, deletion, and upgrade

- [ ] Add the four prevention tables to `database.BusinessTables` in dependency order; backup manifest, `pg_restore --list`, and restore row-count checks then cover them.
- [ ] Add four fields to `DeleteCounts` and `scanDeleteCounts`; update the deletion view in `internal/webshell/stats.go` and `ui.FormatDeleteCounts` so counts are not silently dropped.
- [ ] `TestBackupRestoreIntoEmptyDatabase` seeds an artifact, two versions, issue link, and passed/failed verifications before backup. After restore, compare state, hashes, commit, links, verifications, and audit field by field.
- [ ] Project deletion preview and execution return identical counts for all four new row kinds, retain no cross-issue links, preserve audit_log, and add one delete.scope record.
- [ ] Connector-only, before-only, and retention tests prove active artifacts are not silently deleted. After expired event/occurrence deletion, metrics use remaining real occurrences rather than fabricated history.
- [ ] Update `spec/compat.cue` upgrade guarantees from 0008 to 0009, retain direct 0007→0009 and future-version rejection in the matrix, and synchronize `db/migrations/README.md`.
- [ ] Rerun batch 1's 0007→0009 and 0010 rollback tests to prove final documentation matches actual migration behavior.

Operations acceptance:

```powershell
go test ./internal/database -run 'TestBackupRestoreIntoEmptyDatabase|TestDelete|TestRetention|TestUpgrade' -count=1 -p 1 -v -timeout 15m
go test ./internal/ui ./internal/webshell -run 'Delete|Settings|Stats' -count=1 -v
```

Expected: real pg_dump/pg_restore does not SKIP, all four tables and constraints restore, and preview/execution counts agree.

## 5. Bilingual documentation

- [ ] Add English and Chinese Prevention guides covering four kinds, state machine, six CLI commands, three MCP tools, Git prerequisites, metric definitions, troubleshooting, and a complete example.
- [ ] State that verify/activate are human CLI decisions, MCP register neither reads files nor proves hashes, and Web is read-only.
- [ ] Synchronize README feature overview, commands, MCP count, and documentation index. Keep current v0.1.3 release status intact and label new source capabilities Unreleased.
- [ ] Add actual call chains and file mapping to implementation notes. Project status distinguishes implemented/verified/unreleased without invented dates or tags.
- [ ] Privacy docs cover the 1 MiB limit, no stored bodies or absolute repository paths, argv/evidence redaction, bounded output, and environment whitelist.
- [ ] Update only `CHANGELOG.md` Unreleased with capabilities, 0009 upgrade, operations compatibility, and known limitations. Align every English and Chinese semantic item.

## 6. Final full acceptance

- [ ] After focused tests pass, run every quality gate. Database tests must not skip for missing environment.

```powershell
gofmt -w cmd/errkb internal tests/e2e
go vet ./...
go test ./... -count=1 -p 1 -timeout 20m
cue vet ./spec/...
cue export ./spec -e compat --out json
pnpm run typecheck
pnpm run lint
pnpm run format:check
pnpm test
pnpm run build
git diff --check
```

- [ ] In PR/CI, confirm the `compat` job demonstrates all 19 rules and accepts the R10/R5 classifications. Local `cue export` is not a substitute for the full compatibility gate.
- [ ] Run `./packaging/windows/build.ps1 -Version 0.0.0-prevention-acceptance`, then `./dist/errkb.exe version` and `./dist/errkb.exe mcp selftest`. This is a local acceptance build, not a release.
- [ ] Manually review E2E evidence: real Git state, real PostgreSQL rows, 12 MCP tools, read-only Web sections, restored backup, and logs without required SKIP.
- [ ] Check every non-goal in the design: no generated tests, automatic file/Git writes, model calls, external network, team cloud, or invented prevented counts.

## 7. Final handoff

The handoff is evidence, not merely code: five batches of tests, 0009 upgrade/rollback, real CLI/Git loop, MCP/Web boundaries, restore/deletion results, and bilingual Unreleased docs. The owner separately decides whether to commit, push, number, or publish a release.
