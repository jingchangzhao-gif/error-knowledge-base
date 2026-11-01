# Changelog

All notable changes to errkb are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Sections are written for the people using errkb: what the release can do, what
changed, what breaks, what was fixed, and what is known to be missing or broken.
They never claim more than the acceptance records in this repository prove.

## [Unreleased]

### Added

- **Local prevention loop.** Four repository-backed artifact kinds (regression test,
  deterministic check, agent instruction and intent) gain immutable content-hash versions,
  same-project issue links, explicit verification runs and draft/verified/active/deprecated
  states. Six `errkb prevention` commands cover add, verify, activate, list, show and stats.
- **Human-controlled execution evidence.** CLI verification hashes a regular repository file
  before and after a bounded 120-second command, stores redacted bounded evidence and records
  real failures. Activation requires a matching latest pass and a clean artifact tracked at
  Git `HEAD`; errkb performs no automatic file or Git writes.
- **Prevention interfaces.** MCP adds `err_prevention_register`, `err_prevention_list` and
  `err_prevention_get` (twelve tools total), while executable verify/activate transitions stay
  CLI-only. The Web toolbox adds a read-only prevention statistics area.
- **Observed recurrence metrics.** SQL reports distinct artifact/version counts, verification
  pass ratios, per-issue/version 30-day windows, observing/mature states, all-time recurrence
  and a nullable mature protection rate. No unobservable `prevented_count` is invented.
- **Migration 0009.** Adds the four prevention tables, immutability/state constraints and
  recurrence indexes. Real PostgreSQL tests cover 0007→0009 upgrade, failed 0010 rollback and
  rejection of unknown future ledgers.

### Changed

- Backup manifests, `pg_restore` verification and empty-database restores now include all four
  prevention tables and their audit/history rows.
- Scoped deletion previews, execution, audit details and UI summaries report prevention rows.
  Project deletion removes the full chain; connector-only, time-only and retention operations
  preserve active artifacts, and metrics use the remaining real occurrences.
- Added bilingual prevention guides covering the workflow, Git prerequisites, metrics, privacy,
  troubleshooting and current interface boundaries.

### Known limitations

- Prevention capabilities are source-level Unreleased changes, not part of v0.1.3. Activation
  records evidence but does not automatically wire an artifact into CI or a host. MCP register
  accepts caller-supplied hashes without reading or proving file content; Web prevention views
  are read-only. Complete PR/CI compatibility and release gates remain required before release.

## [0.1.3] - 2026-10-25

### Fixed

- **First double-click starts the toolbox.** `start-errkb.cmd` now offers to create the local
  PostgreSQL cluster on the first run and only starts the existing one afterwards.
- **The Windows smoke job installs `go-winres`** before building the portable executable.
- **The npm publish step is gated through the job environment**, so a missing token is a
  readable skip instead of a workflow that cannot start.

## [0.1.2] - 2026-10-25

### Added

- `errkb mcp install --host codex|claude|dsh` registers the MCP server with a
  local host: the measured `codex mcp add` and `claude mcp add --scope user`
  commands are run for you, DSH prints its plugin path. `--print` shows the
  command, `--json` reports one row per host.
- `errkb mcp selftest` answers one `initialize` and one `tools/list` in memory
  and exits non-zero when a tool is missing; `errkb doctor` now runs the same
  check as its ninth read-only finding.
- `errkb mcp` resolves the installed `pg\credentials.json` and `migrations\`
  itself and starts the daemon when the pipe is unreachable, so a host only
  needs `errkb.exe mcp` as its MCP command; `--no-daemon` keeps the old
  unavailable-error behaviour.
- The daemon enforces the capture gate: when `capture.enabled` is false, or the
  connector or project is paused in `config.yaml`, `record` and `capture` calls
  fail with a `conflict` error instead of writing. Before this change the
  setting only disabled the DSH adapter, so other hosts could still write.
- An event that arrives while the database is unreachable is spooled to the
  daemon's offline queue (already redacted) and answered with `queued: true`
  carrying the event id and fingerprint; the queue replays those events when
  the database recovers. `errkb run` and `errkb record` now say the event was
  queued instead of naming an issue that does not exist yet.
- `err_get_issue` is the ninth MCP tool: the host can read one issue with its
  occurrences, solution versions, outcome history and current trust before
  writing, instead of stitching a lookup and several guesses together.
- `err_list` accepts an `environment` filter (`os`, `shell`, `connector`,
  `runtime_versions`); only the keys set take part in the comparison, so
  "issues seen on Linux with Node 20" is one call rather than client-side
  filtering.

### Changed

- The supported distribution is the portable `errkb-toolbox.zip` (plus a
  standalone EXE). The installer, its acceptance scripts and the installer
  cards are superseded: `errkb pg install` creates the cluster,
  `autostart.ps1` writes the per-user Run key and `start-errkb.cmd` opens the
  toolbox.
- `errkb db`, `errkb doctor`, `errkb markdown` and `errkb mcp` resolve the
  installed `pg\credentials.json` and `migrations\` when they sit next to the
  EXE, so a freshly unpacked toolbox needs no flags.
- `errkb run` defaults `--connector` and `--session` to `cli`, so a bare
  `errkb run -- <command>` records again.
- CI: the Windows acceptance job became a Windows smoke job (build, real
  PostgreSQL, the full Go suite, version and MCP selftest). The release
  workflow no longer builds an installer and adds an npm publish job that runs
  when `NPM_TOKEN` is configured.

### Removed

- `packaging/windows/installer.iss`, `ci-acceptance.ps1`, `vm-acceptance.ps1`,
  `recovery-acceptance.ps1`, `release-test.ps1`, the installer parameters of
  `release.ps1` and `npm run test:release`.

### Fixed

- Silent uninstall with `DELETE_DATA` now removes the cluster, logs and health
  file; the uninstaller passes the choice through a marker file instead of
  losing it while cloning arguments.
- `errkb mcp` no longer requires a running daemon or explicit credentials.

## [0.1.1] - 2026-10-23

Patch release for the installed product: the released builds now work without
explicit connection flags, and the uninstaller honours the silent delete-data
request.

### Fixed

- `errkb db status`, `db migrate`, `doctor`, `ui --web` and the daemon resolve
  `pg\credentials.json` and the bundled `migrations` directory next to the
  installed `errkb.exe`, so a fresh install needs no `--dsn` or `--credentials`
  flags on the first run.
- Silent uninstall: `unins000.exe /VERYSILENT /DELETE_DATA=1` now removes the
  cluster and application data. The previous build expanded
  `{param:DELETE_DATA|keep}` in the installer's second phase, where the value is
  unavailable, so the data was always kept. The choice is now recorded as a
  marker file and read by `postgres.ps1`; a successful install clears stale
  markers.
- The search API reports why a request body was rejected instead of always
  saying the query was empty.
- `docs/install.md` documents the silent uninstall switches and the zero-config
  first-run behaviour; stale "no Web shell" troubleshooting rows were removed.

## [0.1.0] - 2026-10-23

The first release. It is a local error knowledge base: capture the failure once,
see the known fix next time.

### Added

- WF-011A runs the real Windows installer through install, migration, error
  reuse, keep-data uninstall/reinstall and delete-data uninstall on an
  ephemeral Windows runner, with structured evidence uploaded for every run.
- WF-011B deterministically kills and recovers the daemon and managed
  PostgreSQL cluster, then verifies one daemon, the Named Pipe, health ledger
  and saved knowledge. The tag workflow repeats both acceptance cards before
  release packaging.
- `errkb ui --web` serves an embedded loopback toolbox with a per-launch token
  and a strict content security policy. It covers search, issue detail and
  version history, solution edits and status changes, confirmation-gated
  deletion, statistics, audit filters, and connector/privacy settings.
- The release bundle carries `errkb-toolbox.zip` beside the installer, with its
  hash in `SHA256SUMS` and its reproducibility boundary in the manifest.
- The installer registers a Start-menu shortcut for the toolbox and ships its
  launcher.
- `errkb tail --file <path>` follows a log file across rotation and truncation,
  detects UTF-8/UTF-16, bounds duplicate bursts and records classified failures
  through the daemon as channel C.
- `errkb markdown import --file <path>` and
  `errkb markdown export [--out <path>]` expose the legacy `ERRORS.md` exchange
  through the CLI with an idempotent import/export/re-import round trip.
- The trust-formula test owns its schema so parallel test packages cannot race
  on a shared function count.

- `errkb` command-line client: `search`, `show`, `record`, `list`, `feedback`,
  `stats`, `run -- <command>`, `daemon`, `ui`, `doctor`, `mcp` and `version`.
  Human output and `--json` share one contract, and JSON consumers get the
  validated payload instead of a re-rendered approximation.
- Local PostgreSQL knowledge base: exact lookup by issue id, fingerprint, error
  code or command first; approximate candidates are always marked as
  approximate and never auto-reused. Retrieval stays under the p95 150 ms gate
  at 100,000 issues on the reference machine (see `docs/benchmarks.md`).
- `errkb run -- <command>` wrapper: runs the command, keeps its output visible,
  and records only failures; transient network failures are skipped so they do
  not create noise.
- `errkb daemon`: single instance per Windows user session, graceful shutdown,
  a machine-readable health document, and logs that are redacted and rotated.
- `errkb doctor`: eight read-only checks (version and database, migrations,
  optional `pg_trgm`, Named Pipe, connectors, offline queue, privacy patterns,
  data directory writability) with a readable fix per finding.
- Tray icon, browser-style search window, desktop notification model, issue
  detail and solution editor, connector and privacy settings page, and metrics
  and audit views.
- Connectors: DSH plugin (injected known-fix hints, request-error diagnosis
  hand-back through `errkb` tools), Codex wrapper plus MCP registration, Claude
  Code hook plus wrapper, and a generic CLI/log template for other hosts.
- MCP stdio server with eight tools for hosts that speak MCP.
- Offline queue: failures recorded while the daemon is down are kept on disk
  (bounded) and replayed in arrival order when it returns.
- Backup, restore into a fresh database, retention classes and scoped deletion
  with an audit trail.
- Windows packaging: `errkb.exe` with version resources, a reproducible build,
  an Inno Setup installer that creates a dedicated PostgreSQL cluster on port
  5433, and a clean-VM acceptance harness.

### Changed

- The DSH plugin is now a thin adapter over the local core: capture, lookup and
  notice delivery go through the daemon, so the CLI, MCP and the plugin share
  one retrieval path and one trust signal.
- Fix trust is computed by the core (`errkb_trust()`); the plugin no longer
  keeps its own trust counters.
- The plugin package ships its built `lib/` entry points so the package can be
  reviewed and loaded as a fixed source snapshot.

### Breaking changes

- Not applicable: this is the first release. The database schema is versioned
  and upgrades from every earlier migration are covered by
  `internal/database/upgrade_test.go`; a newer schema or protocol version is
  refused explicitly rather than guessed at.
- The one planned breaking change for the next minor version: the state file
  format changed with the offline queue. The development state is migrated in
  place when it can be read; unreadable files are set aside, not deleted.

### Fixed

- The release gate resolves the real `refs/tags/v<version>` Git ref, requires
  it to point to a clean HEAD, and rejects an explicit build commit that does
  not match the tag.
- Setup applies database migrations (`errkb db migrate`) before the first run.
- Logon recovery uses the per-user Run key instead of an administrator-only
  Scheduled Task.
- The PostgreSQL lifecycle has one implementation: `errkb pg` creates, starts,
  stops and uninstalls the local cluster; `postgres.ps1` only forwards installer
  arguments.
- Silent reinstall reuses retained cluster data without opening an interactive
  prompt, and delete-data uninstall removes the complete per-user app directory.

- The Windows store lock no longer fails under the concurrent tests: exclusive
  creation treats `EPERM`/`EBUSY` as contention, and the delete-pending read
  path tolerates `ENOENT` and `EPERM`.
- Long log lines and repeated failures no longer flood the knowledge base: the
  tail records bounded duplicate bursts and the wrapper skips transient
  failures.
- The redaction rules are generated from one source (`spec/redact.cue`) into
  Go, the TypeScript patterns and the privacy guard, so the three can no longer
  drift.
- Request-id patterns no longer reject the redacted label
  `request_id: <request-id>`.
- The Windows installer no longer expands `{app}` before the directory page is
  initialised, and the cluster password generator works under Windows
  PowerShell 5.1.
- The CI database job applies only the top-level migrations and no longer
  replays the historical fixtures in `db/migrations/testdata`.

### Known issues

- Physical reboot and interactive clean-machine visual review are optional
  manual evidence rather than continuously automated checks. WF-011B covers
  equivalent daemon/PostgreSQL/logon recovery deterministically in CI.
- The release is not code-signed. Verify the artifacts against `SHA256SUMS`;
  `SIGNING.md` in the bundle states the risk and the plan.
- PostgreSQL is a runtime prerequisite and is not bundled. The knowledge base
  needs a local PostgreSQL 16 instance; the installer can create a dedicated
  cluster on port 5433.
- `errkb run -- <command>` without any option before `--` reports that the
  command must follow `--` even though it does. Pass `--connector` and
  `--session` (the adapters do), or use `errkb run --connector <id> -- <cmd>`.
- The DSH plugin is pending the store review; installing it by hand is the
  supported path for now.

[Unreleased]: https://github.com/jingchangzhao-gif/error-knowledge-base/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.0
