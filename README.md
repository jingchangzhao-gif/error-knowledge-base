# Error Knowledge Base

[简体中文](README.zh-CN.md) · [License](LICENSE)

> **Recycles errors: the same failure is diagnosed once, recorded, and handed back as a known fix the next time it appears — locally, without calling a model.**

**Project status (2026-10-25): v0.1.3 released.** The reusable core, PostgreSQL schema, CLI, daemon, MCP server, measured DSH/Codex/Claude connectors and embedded Web toolbox ship as a **portable toolbox ZIP** (plus a standalone EXE); the installer path was removed. Download `errkb-toolbox.zip` from the [latest release](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest).

The **Error Knowledge Base** (`error-knowledge-base`) is a local platform with several interfaces: a command-line client, a background daemon, an MCP stdio server, a [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) plugin, host connectors and an embedded Windows Web toolbox. Every interface uses the same core and the same knowledge base; classification, redaction, normalisation, fingerprinting, storage, retrieval and fix trust are not reimplemented per host. The command remains `errkb`, and the DSH adapter package retains the name `dsh-errkb`.

## Contents

- [What the Error Knowledge Base is](#what-the-error-knowledge-base-is)
- [Current delivery status](#current-delivery-status)
- [Feature overview](#feature-overview)
- [How it works](#how-it-works)
- [Supported hosts (measured)](#supported-hosts-measured)
- [What the core guarantees](#what-the-core-guarantees)
- [Quick start](#quick-start)
- [Command line](#command-line)
- [Configuration](#configuration)
- [Requirements](#requirements)
- [Compatibility and version caliber](#compatibility-and-version-caliber)
- [Installing and uninstalling](#installing-and-uninstalling)
- [Privacy](#privacy)
- [Documentation](#documentation)
- [Known limitations](#known-limitations)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## What the Error Knowledge Base is

A local error knowledge base for LLM tools and command-line workflows. When something fails, it records the failure once — redacted, normalised and fingerprinted — in a local PostgreSQL database. The next time the same failure appears, it hands back the recorded fix, with the match reason, the applicability warning when the environment differs, and the fix's measured trust.

It is deliberately **not**:

- **A chat model.** The core never calls a model and makes no outbound network request. Connectors observe what the host already produced; the fix either already exists in the database or is written by the user. Local PostgreSQL, Named Pipe and an explicitly enabled loopback HTTP listener are the only transports.
- **A log archive.** Only failures that earn an ID are stored, with bounded samples; retention is explicit.
- **A cloud service.** There is no account, no telemetry and no remote endpoint. HTTP off by default; the local API is a Named Pipe.
- **A drop-in binary with an external database.** The knowledge base is PostgreSQL, a runtime prerequisite (see [Requirements](#requirements)); `errkb pg install` creates and manages a dedicated cluster on port 5433.

## Current delivery status

The project uses three different words deliberately:

- **Implemented** means source code and automated tests exist.
- **Verified** means the behavior has been exercised in the stated real environment.
- **Releasable** means one coherent, versioned artifact has passed the mandatory Windows lifecycle/recovery and provenance gates.

| Area                                      | Current state                                               | What that means                                                                                                                                                                              |
| ----------------------------------------- | ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Deterministic core and redaction          | Implemented and unit-tested                                 | Normalisation, fingerprinting, matching and redaction can be developed normally                                                                                                              |
| PostgreSQL schema and retrieval           | Implemented; integration tests require a real database      | v0.1.3 contains `0001`–`0008`; Unreleased source adds `0009` prevention metadata, with exact/fuzzy retrieval, solutions and outcomes                                                         |
| CLI, daemon and MCP                       | Implemented                                                 | Usable from a development build when a migrated PostgreSQL instance and DSN are available                                                                                                    |
| DSH, Codex CLI and Claude Code connectors | Measured with pinned versions                               | See the connector matrix; unsupported host behavior is not implied                                                                                                                           |
| Portable release and Windows recovery     | Implemented; the Windows smoke job runs the same executable | The Windows CI job builds the binary, runs every Go test against a real PostgreSQL and answers the MCP self-test; `errkb pg install` plus `autostart.ps1` cover first run and logon recovery |
| Embedded Web toolbox                      | Implemented and automated-test covered                      | Search, detail/edit, statistics/audit and settings routes are wired through `ui --web`                                                                                                       |
| Release provenance                        | Implemented and release-verified                            | Missing/mismatched tags and dirty source are rejected; accepted tag `v0.1.1` points to commit `1438473`                                                                                      |
| Release bundle                            | Released as v0.1.3                                          | The GitHub Release contains the toolbox ZIP, the standalone EXE, manifest, SBOM and checksums; the npm package is published from the same tag when the repository has an `NPM_TOKEN` secret  |

## Feature overview

The product covers one complete technical-error reuse loop rather than only collecting logs:

| Stage                | User-visible capability                                                                                                 | Implementation boundary                                                                                                  |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Observe              | Capture failures from hooks, CLI wrappers, connectors and logs                                                          | Adapters emit `ErrorEvent v1`; they do not own matching logic                                                            |
| Protect              | Redact secrets before persistence                                                                                       | One CUE rule source generates Go, TypeScript and CI rules                                                                |
| Identify             | Classify, normalize, fingerprint and deduplicate                                                                        | Deterministic Go/TypeScript processing plus transactional PostgreSQL recording                                           |
| Retrieve             | Search exact identifiers first, then bounded approximate candidates                                                     | PL/pgSQL exact/`pg_trgm` queries and Go Jaccard reranking                                                                |
| Reuse                | Return a stored fix with source, match reason, environment warning and trust                                            | CLI, MCP, notifications and UI use the same backend contract                                                             |
| Learn                | Record whether a fix succeeded and keep solution history                                                                | Versioned solutions, outcomes and SQL-computed trust                                                                     |
| Prevent (Unreleased) | Version regression tests, deterministic checks, agent instructions and intents; verify, activate and observe recurrence | Human CLI decisions, immutable metadata, clean Git evidence and real occurrence statistics; no automatic file/Git writes |
| Operate              | Diagnose, migrate, retain, delete, audit and back up local data                                                         | Go operations layer plus PostgreSQL functions and migration ledger                                                       |

The source-level mapping for every feature, including what is complete and what is only modeled, is in [`docs/features-and-implementation.md`](docs/features-and-implementation.md).

## How it works

```text
host event ──▶ adapter ──▶ redact ──▶ normalise ──▶ fingerprint ──▶ PostgreSQL
   (hook, wrapper, MCP)         (before any write)                   │
                                                                    ▼
fix + reason + trust ◀── exact match first, approximate top 3 ◀──── retrieval
```

1. **Capture.** A connector turns an observable failure into an `ErrorEvent v1` payload: structured fields where the host reports them (exit code, command, tool name, session), derived fields otherwise (headline, category, severity).
2. **Redact before write.** Secrets, credentials, e-mail addresses and absolute home paths are replaced with placeholders by one rule source (`spec/redact.cue`), generated into the Go engine, the TypeScript adapter and the CI privacy guard.
3. **Normalise and fingerprint.** ANSI escapes, GUIDs, timestamps, PIDs, counters, ports and absolute paths are removed; the remaining text is lower-cased and hashed as `sha256(category + "\0" + normalised)[:12]`. The same failure yields the same 12-hex fingerprint; different failures do not.
4. **Store.** Issues (one per fingerprint and project), occurrences, immutable events, solution versions and outcomes land in PostgreSQL. Schema migrations run in one transaction each, with an advisory lock and a checksum ledger.
5. **Retrieve and hand back.** Exact lookups by issue id, fingerprint, error code or command run first. Fuzzy candidates come from `pg_trgm`, are re-ranked with Jaccard similarity, and are always capped at three and **marked approximate**. The result carries the match reason, the environment difference when one exists, and the fix's trust; approximate matches are never auto-reused.

For the source-level implementation map, see [`docs/features-and-implementation.md`](docs/features-and-implementation.md). For the ordered construction plan, see [`docs/implementation-plan.md`](docs/implementation-plan.md).

## Supported hosts (measured)

Measured on 2026-10-20; versions are pinned on purpose. Each row maps to a real acceptance record, and anything not measured is listed as unverified in [`docs/connector-matrix.md`](docs/connector-matrix.md).

| Host                         | Measured version     | Channel                                                          | Known fix before diagnosis                            | Acceptance record                                          |
| ---------------------------- | -------------------- | ---------------------------------------------------------------- | ----------------------------------------------------- | ---------------------------------------------------------- |
| Generic CLI (`errkb run --`) | errkb from this tree | **B** wrapper                                                    | No — after the failure                                | [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md)             |
| DSH (plugin adapter)         | DSH 0.2.0-rc.2       | **A** plugin hooks                                               | Yes — standing hint and post-execute notices          | EKB-039, PR #66                                            |
| Codex CLI                    | 0.156.1              | **B** wrapper; **MCP** 12 tools in Unreleased source (v0.1.3: 9) | No — MCP tools are available, but no pre-failure hook | [`connectors/codex/probe.md`](connectors/codex/probe.md)   |
| Claude Code                  | 2.1.282              | **A** `PostToolUseFailure` hook; **B** wrapper; **MCP**          | Yes — `SessionStart` hint and MCP tools               | [`connectors/claude/probe.md`](connectors/claude/probe.md) |

Generic log and manual hosts use the template in [`connectors/templates/cli-log/`](connectors/templates/cli-log/README.md); their log channel and manual channel are not yet measured live. A window title is not an interface contract, so no support claim here rests on one.

## What the core guarantees

- **Exact before fuzzy.** The retrieval gate asks for one whole value — issue id, fingerprint, error code, normalised message, command or tool name — and only then widens.
- **No false auto-merge.** A match is auto-reusable only when it is an exact fingerprint match and the code, command and tool do not contradict. The labelled corpus (`spec/corpus/labelled/`) contains lookalikes that a naive matcher merges; the test asserts zero false auto-merges.
- **Approximate matches are visible.** The UI prints `APPROXIMATE — confirm before reuse`; notices carry the badge and the environment warning; MCP and CLI JSON carry `approximate: true`.
- **Top three, with reasons.** Retrieval returns at most three candidates, each with the match reason and the fields that matched.
- **Latency has a gate, not an aspiration.** On the reference machine at 100,000 issues: exact p95 0.6 ms, trigram candidates p95 81.7 ms, full retrieval p95 84.5 ms (gate: 150 ms). Environment, dataset and plans are in [`docs/benchmarks.md`](docs/benchmarks.md).
- **Trust is computed, not asserted.** `errkb_trust()` = `(successes + 2) / (successes + failures + 4)`, computed in SQL from recorded outcomes; unknown outcomes are excluded. The UI displays it and cannot write it.
- **Writes are idempotent.** Events are unique by `event_id`; connector observations carry an `observation_id`; replays do not create duplicates, and occurrences are always appended.
- **Nothing crashes the product.** The daemon is single-instance per Windows session, shuts down gracefully, writes a health document, and keeps a redacted rotating log. A crash during a write, a batch or a migration leaves committed data intact (verified by killing real processes in the test suite).

## Quick start

The supported release is the portable toolbox ZIP: `errkb.exe` plus `start-errkb.cmd`, `autostart.ps1` and `postgres.ps1`. There is no installer; unpack the ZIP somewhere you own.

1. Install **PostgreSQL 16** once (EDB installer). errkb uses its binaries to create and manage its own cluster.
2. Unpack `errkb-toolbox.zip`, then create the cluster and the schema:

   ```powershell
   .\errkb.exe pg install
   ```

   This starts a dedicated cluster on port 5433 under `.\pg\data`, applies migrations and writes `.\pg\credentials.json`. To reuse an existing server instead, run `errkb db migrate --dsn <your-dsn>`.

3. Open the toolbox with `start-errkb.cmd` (or `.\errkb.exe ui --web`); it starts the daemon itself.
4. Optional logon recovery:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -Action install -AppDir "$PWD"
   ```

5. Capture a real failure and reuse the fix:

   ```powershell
   .\errkb.exe run --connector cli --session demo --project demo -- cmd /c "exit 9"
   ```

   Record the fix through the MCP server (`err_record_solution`) or a connector, run the same command again, and the wrapper prints the known fix, its source and its trust.

The full loop, including the recorded output, is in [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md); the portable install steps are in [`docs/install.md`](docs/install.md).

## Command line

One binary, several subcommands:

| Command                                                                | What it does                                                                                                                                     |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `errkb search <query>`                                                 | Search by issue id, fingerprint, error code, command or text; human table or `--json`                                                            |
| `errkb show <id-or-fingerprint>`                                       | One issue with occurrences, solutions and versions                                                                                               |
| `errkb record --event <file>`                                          | Record a complete `ErrorEvent v1` payload (from stdin or a file)                                                                                 |
| `errkb list --kind issue\|solution`                                    | List issues or solutions, filterable by project                                                                                                  |
| `errkb feedback <issue> <version> --outcome success\|failure\|unknown` | Record an outcome; this is what moves trust                                                                                                      |
| `errkb stats`                                                          | Counts, hit rate, latency percentiles and estimated savings                                                                                      |
| `errkb prevention add\|verify\|activate\|list\|show\|stats`            | Unreleased source: register, verify, activate and observe versioned prevention artifacts; see the prevention guide                               |
| `errkb run -- <command>`                                               | Run a command, keep its output visible, record failures only                                                                                     |
| `errkb db migrate\|status`                                             | Apply migrations or inspect the schema ledger                                                                                                    |
| `errkb tail --file <path>`                                             | Follow a log file and record its failures (channel C)                                                                                            |
| `errkb markdown import\|export`                                        | Exchange the legacy `ERRORS.md` format                                                                                                           |
| `errkb daemon`                                                         | Local background service: Named Pipe API, health document, redacted log                                                                          |
| `errkb ui`                                                             | Native tray/search host from the source tree; not the released Web toolbox                                                                       |
| `errkb ui --web`                                                       | Local Web toolbox (search, detail/edit, statistics, settings); shipped in the toolbox ZIP                                                        |
| `errkb doctor`                                                         | Nine read-only environment, connector and MCP checks, with a fix per finding                                                                     |
| `errkb mcp`                                                            | MCP stdio server with 12 tools in Unreleased source (v0.1.3: 9); resolves the installed credentials and starts the daemon when it is not running |
| `errkb mcp install --host codex\|claude\|dsh`                          | Register the MCP server with a local LLM host; DSH prints its plugin path instead                                                                |
| `errkb mcp selftest`                                                   | Answer one initialize and tools/list in memory, then exit; `doctor` runs it                                                                      |
| `errkb version`                                                        | Version, commit, build time and Go toolchain                                                                                                     |

Exit codes: `0` found/success, `1` internal error, `2` bad arguments, `3` not found, `4` daemon unavailable. `--json` output is the validated contract payload, not a re-rendered approximation.

Registering the MCP server is one command per host; in every case the server starts the daemon from the installed credentials and never needs a manually managed pipe:

```powershell
errkb mcp install --host codex     # Codex: writes the MCP entry
errkb mcp install --host claude    # Claude Code: writes the user-scope entry
errkb mcp install --host dsh       # DSH integrates through the plugin; prints that path
errkb mcp selftest                 # initialize + tools/list in memory, prints the tool count
```

`--print` shows the commands without running them. `errkb doctor` includes the MCP check, so a broken tool table shows up before a host does.

## Configuration

Configuration is a YAML document validated against one contract (`spec/config.cue`):

```yaml
database:
  dsn: postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable
service:
  pipe:
    enabled: true
  http:
    enabled: false # loopback-only when enabled, token at least 32 characters
capture:
  enabled: true
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
```

Defaults: Named Pipe on, HTTP off, capture on, notices on, search limit 3, capture/search/MCP timeouts 2000/1500/5000 ms. The daemon enforces them: while `capture.enabled` is false, or a connector or project is paused, `record` and `capture` calls fail with `conflict` instead of writing, and events that arrive while the database is unreachable are queued (already redacted) and replayed on recovery. The daemon also takes explicit flags (`--dsn`, `--config`, `--queue-dir`, `--pipe`, `--health-file`, `--log-file`, …); the settings page model edits the same file without introducing a second configuration model. A full example ships in [`connectors/templates/cli-log/config.yaml`](connectors/templates/cli-log/config.yaml).

## Requirements

- **Windows 10 or 11, x64.** The daemon, tray, Named Pipe API and Windows connector service are Windows-specific.
- **PostgreSQL 16** (server). Not bundled; `errkb pg install` creates a dedicated cluster on port 5433 using the installed PostgreSQL binaries. A running local instance can be reused instead.
- **No Go, Node.js or development toolchain at runtime.** The release is one `errkb.exe` plus small PowerShell scripts; PostgreSQL is the only external prerequisite.
- Development only: Go 1.26.4, Node.js 24.16.0 with pnpm 11.7.0, CUE 0.17.1, and a local PostgreSQL for the database tests.

## Compatibility and version caliber

- **Schema.** v1. Released v0.1.3 contains migrations `0001`–`0008`; Unreleased source adds `0009` for prevention metadata. Upgrades from older schemas are tested against real snapshots in `db/migrations/testdata/upgrade/`; a newer ledger version is refused explicitly, and a failed migration rolls back in one transaction.
- **Contracts.** CUE definitions for `ErrorEvent v1`, config, connector capability, MCP tools, the local API, the connector SDK and the redaction rules. Compatibility is enforced by `spec/compat.cue` and the `Schema compatibility` CI job: additive optional fields and new enum members are compatible; removing, renaming or re-typing a field requires a `schema_version` bump, and a broken rule fails the job.
- **DSH packages.** The plugin builds against `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools` **0.1.5-rc.3** as resolved by the lockfile, and a DSH host in the **0.2.x** generation (0.2.0-rc.2 measured; the plan records `dsh-ops`'s peer range as the version evidence). The earlier rc.2 statement was a historical design-time caliber: the peer range `^0.1.5-rc.2` covers rc.3, and the API surface the adapter uses did not change. The full explanation is plan §7.2.
- **Protocols.** MCP `2024-11-05`; local API and connector SDK envelopes are `v1`. Unknown fields are rejected on decoded payloads, and a payload declaring a newer version is refused rather than guessed.

## Installing and uninstalling

Releases are portable: download `errkb-toolbox.zip`, unpack it into a directory you own, and run `errkb.exe pg install` once. The cluster lives in `pg\data`, credentials in `pg\credentials.json` (mode 0600), and logs/health in the same directory; `autostart.ps1 -Action install` writes the per-user Run key for logon recovery. To uninstall, run `errkb.exe pg uninstall -DeleteData` (omit `-DeleteData` to keep the database), then remove the Run entry with `autostart.ps1 -Action uninstall` and delete the folder. The release is not code-signed; verify artifacts against `SHA256SUMS` from the release page.

Full steps, data locations and uninstall behavior are in [`docs/install.md`](docs/install.md).

## Privacy

Everything stays on the machine. The core never calls a model and makes no outbound network request; the local API is a Named Pipe with an owner-only ACL, HTTP is off by default, and redaction happens **before** anything is written to the knowledge base. Retention periods, scoped deletion with a preview and an audit trail, and what the adapter's offline queue keeps are described in [`docs/privacy.md`](docs/privacy.md).

## Documentation

| Document                                                                                                                         | What it answers                                                                                   |
| -------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| [`docs/project-status.md`](docs/project-status.md) / [中文](docs/project-status.zh-CN.md)                                        | What is implemented, verified, blocked and safe to run now                                        |
| [`docs/workflow/README.md`](docs/workflow/README.md) / [中文](docs/workflow/README.zh-CN.md)                                     | Workflow control center: active issues, file repair order and cleanup audit                       |
| [`docs/features-and-implementation.md`](docs/features-and-implementation.md) / [中文](docs/features-and-implementation.zh-CN.md) | What every feature does and which code, contract and data path implements it                      |
| [`docs/implementation-plan.md`](docs/implementation-plan.md) / [中文](docs/implementation-plan.zh-CN.md)                         | Scope, decisions, ordered task cards and acceptance gates                                         |
| [`docs/connector-matrix.md`](docs/connector-matrix.md) / [中文](docs/connector-matrix.zh-CN.md)                                  | Which host reports which field, measured with pinned versions                                     |
| [`docs/install.md`](docs/install.md) / [中文](docs/install.zh-CN.md)                                                             | Current installation candidate, data locations, upgrade and uninstall                             |
| [`docs/toolbox-ui.md`](docs/toolbox-ui.md) / [中文](docs/toolbox-ui.zh-CN.md)                                                    | Toolbox UI architecture, implementation status and remaining cards                                |
| [`docs/privacy.md`](docs/privacy.md) / [中文](docs/privacy.zh-CN.md)                                                             | Data flow, redaction, retention, deletion                                                         |
| [`docs/benchmarks.md`](docs/benchmarks.md) / [中文](docs/benchmarks.zh-CN.md)                                                    | Performance gate, dataset, plans, environment                                                     |
| [`docs/test-record.md`](docs/test-record.md) / [中文](docs/test-record.zh-CN.md)                                                 | Durable full-suite and Windows acceptance runs                                                    |
| [`docs/acceptance-vm.md`](docs/acceptance-vm.md) / [中文](docs/acceptance-vm.zh-CN.md)                                           | Historical clean-machine record; the live gate is the Windows smoke job                           |
| [`docs/e2e-cli-loop.md`](docs/e2e-cli-loop.md) / [中文](docs/e2e-cli-loop.zh-CN.md)                                              | The generic CLI loop, end to end, without a model                                                 |
| [`docs/prevention.md`](docs/prevention.md) / [中文](docs/prevention.zh-CN.md)                                                    | Unreleased prevention artifacts, verification, activation, recurrence metrics and troubleshooting |
| [`CHANGELOG.md`](CHANGELOG.md)                                                                                                   | What changed in each release                                                                      |
| [`packaging/windows/`](packaging/windows)                                                                                        | Build and release scripts for the portable toolbox                                                |

## Known limitations

These are remaining limitations or deliberate boundaries, not hidden capabilities:

- **A physical reboot is not continuously automated.** The suite restarts the daemon and the PostgreSQL cluster and replays the logon command deterministically; a real reboot remains optional manual evidence.
- **The release is not code-signed.** SmartScreen may warn; verify `SHA256SUMS`.
- **PostgreSQL is a prerequisite.** The core needs a local PostgreSQL 16 server; `errkb pg install` creates a dedicated cluster, but the database is not embedded in the application.
- **The DSH plugin is pending store review.** Installing it by hand is the supported path for now.
- **Two measured gaps in the connector matrix:** the DSH `tools/result` live path and the Codex `PostToolUse` hook are unverified.
- **Prevention activation is not host or CI integration.** Activating an artifact does not install a hook, change CI, edit files or write Git state. MCP registration accepts a caller-supplied hash without proving it against a file; the Web prevention view is read-only.

## Development

```powershell
pnpm install
pnpm typecheck     # tsc --noEmit
pnpm lint          # oxlint
pnpm format:check  # prettier
pnpm test          # vitest with the 99% coverage gate
pnpm build         # the DSH connector bundle in lib/

go build ./...
go vet ./...
gofmt -l .
cue vet ./spec/...
```

Database tests need a local PostgreSQL and `ERRKB_TEST_DATABASE_URL`; without it they skip. The build and release scripts are in [`packaging/windows/`](packaging/windows), the task-by-task order is plan §11.1, and the repository conventions (one card per PR, both languages change together) are in the plan.

## Contributing

Read the implementation plan first; work happens card by card (`EKB-nnn`), one pull request per card, with the gates from the plan's §12. Keep the two plan languages and the two README languages in step. Security or privacy issues belong in an issue that names the affected file, not in a public fix commit.

## License

[MIT](LICENSE) © jingchangzhao-gif
