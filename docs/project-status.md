# Project status

Last reviewed: 2026-10-25 against tag `v0.1.3`.

## Current release

`v0.1.3` is the supported public release. The distribution is the portable toolbox:
`errkb-toolbox.zip` (plus a standalone `errkb.exe` in the same release). There is no
installer and no Start-menu entry; unpack the ZIP into a directory you own and run
`errkb.exe pg install` once.

## What ships

| Area              | State                                                                                                                                                                                                                                                                               |
| ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Reusable core     | Normalize, fingerprint, headline, classification, tokenize, Jaccard, rerank, shared redaction, all covered by Go tests                                                                                                                                                              |
| PostgreSQL schema | Migrations 0001–0008; idempotent recording, trust, exact and trigram retrieval, solutions and outcomes, retention, backup/restore                                                                                                                                                   |
| CLI               | `db`, `run`, `tail`, `markdown`, `search`, `list`, `show`, `stats`, `record`, `feedback`, `mcp`, `ui`, `pg`, `doctor`, `version`                                                                                                                                                    |
| Daemon and API    | Named-pipe JSON API with schema-versioned envelopes; capture gate honours `capture.enabled`, paused connectors and paused projects; redacted offline queue replays when PostgreSQL returns                                                                                          |
| MCP server        | Nine tools (`err_lookup`, `err_search`, `err_record`, `err_record_solution`, `err_set_status`, `err_get_issue`, `err_feedback`, `err_list`, `err_stats`); resolves installed credentials and starts the daemon; `errkb mcp install --host …` registers it with Codex or Claude Code |
| Hosts             | DSH adapter (request-error waterfall, providers, dedupe, hand-back), Codex wrapper, Claude Code wrapper; measured in `docs/connector-matrix.md`                                                                                                                                     |
| Web toolbox       | Loopback-only, token + CSP: search, issue detail with audited edits, statistics/audit, settings (connectors, capture, retention, deletion with preview)                                                                                                                             |
| Distribution      | `errkb-toolbox.zip` (errkb.exe, launcher, autostart script, PostgreSQL wrapper, migrations, docs), `SHA256SUMS`, manifest, SBOM, notices, signing policy; `npm install <tarball>` publishes the same tagged code when `NPM_TOKEN` is configured                                     |
| CI                | `ci`, `go`, `cue`, `database`, `windows-filesystem`, `windows-go`, `compat`; the release workflow builds the ZIP and publishes the npm package                                                                                                                                      |

## Unreleased source: prevention loop

The current source contains a prevention loop that is **implemented and locally verified, but not released** in v0.1.3:

| Area                 | Implemented and locally verified behavior                                                                                             |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Contracts and schema | Prevention v1 contracts, migration 0009, immutable versions, issue links, verification history and activation state                   |
| CLI and service      | Six `prevention` commands, real bounded verification, clean/tracked Git activation evidence, stable list/get/stats reads              |
| MCP and Web          | Twelve MCP tools total; register/list/get prevention tools without executable transitions; read-only Web prevention statistics        |
| Metrics              | Verification ratios, per-issue/version 30-day windows, observing/mature states, all-time recurrence and nullable mature rate          |
| Operations           | Four prevention tables in backup/restore and project deletion counts; connector/time deletion and retention preserve active artifacts |
| Upgrade evidence     | Real PostgreSQL 0007→0009 direct upgrade, failed 0010 rollback, and future-ledger rejection tests pass locally                        |

The full PR/CI compatibility classification and 19-rule self-test remain a release/CI gate; local tests do not claim that a new release has passed it. See [`prevention.md`](prevention.md).

## What is verified

- `go test ./...` with a real PostgreSQL, the CUE contracts and the compatibility
  checker (19 rules) run in CI; the Windows smoke job runs the same executable.
- Releases: tag `v0.1.3`, changelog section, binary version/commit/build time, per-file
  `SHA256SUMS`, and the npm tarball are checked by `packaging/windows/release.ps1`.
- Connector behaviour is recorded with versions and dates in
  [`connector-matrix.md`](connector-matrix.md); unmeasured cells are marked.

## Known gaps

- Physical reboot and interactive visual review are not automated; behaviour after a
  logon is exercised deterministically (daemon/PostgreSQL restart plus the Run-key
  command).
- PostgreSQL 16 is a prerequisite. The database is not embedded; `errkb pg install`
  manages a dedicated instance but still needs installed PostgreSQL binaries.
- The release is not code-signed; integrity relies on `SHA256SUMS`.
- Re-adding the DSH STORE catalog entry is pending (store review of the previous
  submission).
- Some host surfaces remain Windows-only (pipe, tray, Win32 windows).

## Getting it

Download `errkb-toolbox.zip` (or `errkb.exe`) from the
[latest release](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest).
Installation, upgrade and uninstall steps are in [`install.md`](install.md); the
installation route is the portable layout described there, not an installer.
