# Error Knowledge Base

[简体中文](README.zh-CN.md) · [License](LICENSE)

> **Diagnose an error once, record the fix, and reuse it the next time the same failure appears — locally, without calling a model.**

Error Knowledge Base (`errkb`) is a local error-reuse and prevention platform for command-line workflows and AI coding tools. It captures observable failures, stores redacted and deduplicated knowledge in PostgreSQL, returns known fixes with evidence and trust, and tracks repository-backed prevention artifacts.

## Release and source status

| Version             | What it contains                                                                                                                              |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| **Released v0.1.3** | Error capture, retrieval, versioned fixes, outcomes and trust; CLI, daemon, Web toolbox and connectors; migrations `0001`–`0008`; 9 MCP tools |
| **Current `main`**  | Everything above, plus the prevention loop, recurrence metrics, migration `0009` and 12 MCP tools; tested but not yet released                |

Download the portable toolbox from [GitHub Releases](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest). Source-only capabilities are marked **Unreleased** below so the download is not confused with the development branch.

## What you can do

| Goal                                | Capability                                                                                                                        |
| ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Capture failures                    | Wrap CLI commands, observe supported host hooks, follow logs or submit an `ErrorEvent v1` payload                                 |
| Protect local data                  | Redact secrets before persistence; use a local Named Pipe by default with no telemetry or outbound model calls                    |
| Identify repeats                    | Classify, normalize, fingerprint and deduplicate failures by project; repeated events append occurrences idempotently             |
| Find known fixes                    | Search exact identifiers first, then at most three explicitly labelled approximate candidates                                     |
| Reuse with context                  | Return the stored fix, source, match reason, environment warning and measured trust                                               |
| Learn from results                  | Keep solution versions and record success, failure or unknown outcomes for a specific version                                     |
| Prevent recurrence (**Unreleased**) | Version regression tests, deterministic checks, agent instructions and intents; verify, activate and measure real recurrence      |
| Operate locally                     | Use the Web toolbox, MCP, diagnostics, migrations, backup/restore, retention, audited deletion and portable PostgreSQL management |

The exact implementation and verification boundary for each feature is in [`docs/features-and-implementation.md`](docs/features-and-implementation.md).

## How it works

```text
CLI / host hook / log / manual event
                │
                ▼
       redact → classify → normalize → fingerprint
                │
                ▼
        local daemon → PostgreSQL
                │
                ▼
 exact match → bounded approximate candidates → fix + reason + trust
```

- Only failures are captured by the CLI wrapper; successful commands are not stored.
- Exact fingerprint matches are reusable only when structured fields do not contradict. Approximate matches always require confirmation.
- Fixes come from stored solution versions, not from a model generating a new answer.
- Trust is computed from recorded outcomes; unknown outcomes do not change it.

## Quick start

### Requirements

- Windows 10 or 11, x64.
- PostgreSQL 16. errkb manages a dedicated local cluster; the database is not embedded.
- No Go, Node.js or development toolchain is needed to run a release.

### Install and open

Unpack `errkb-toolbox.zip` into a writable directory, then run:

```powershell
.\errkb.exe pg install
.\start-errkb.cmd
```

The first command creates the local cluster, applies migrations and writes credentials. The second opens the loopback-only Web toolbox and starts the daemon when needed. For an existing PostgreSQL server, use `errkb db migrate --dsn <your-dsn>` instead.

See [`docs/install.md`](docs/install.md) for custom ports, upgrades, logon recovery and uninstallation.

### Complete the first reuse loop

Run a real project command through errkb:

```powershell
.\errkb.exe run --connector cli --session local --project my-repo -- npm test
```

If it fails, open the issue in the Web toolbox and record the verified fix, or use MCP `err_record_solution`. Run the same command again to retrieve that fix when the failure repeats.

Record whether the specific solution version worked:

```powershell
.\errkb.exe feedback <issue-id> <solution-version-id> --outcome success
.\errkb.exe search "module not found"
.\errkb.exe show <issue-id>
.\errkb.exe stats
```

The generic CLI returns a known fix **after** observing the failure. Supported DSH and Claude hooks can additionally provide standing hints or notices before a new diagnosis.

## AI host integration

| Host        | Supported integration                                               |
| ----------- | ------------------------------------------------------------------- |
| Generic CLI | `errkb run -- <command>` failure capture and known-fix lookup       |
| DSH         | Plugin hooks, standing hints and post-execution notices             |
| Codex CLI   | CLI wrapper and MCP tools; no pre-failure hook is claimed           |
| Claude Code | `PostToolUseFailure` hook, CLI wrapper, session hints and MCP tools |

Register MCP with a supported host:

```powershell
.\errkb.exe mcp install --host codex
.\errkb.exe mcp install --host claude
.\errkb.exe mcp install --host dsh
.\errkb.exe mcp selftest
```

Current `main` exposes 12 MCP tools; released v0.1.3 exposes 9. MCP lets a model or user actively query and update allowed knowledge; installing it does not passively watch the host. Measured versions and unsupported paths are recorded in [`docs/connector-matrix.md`](docs/connector-matrix.md).

## Prevention loop on current `main`

Turn a resolved issue into a reviewed, versioned prevention artifact:

1. Register a repository-relative file and its SHA-256 as a draft.
2. Explicitly run a bounded verification command through the CLI.
3. Commit the artifact yourself, then activate the matching verified version at a clean Git `HEAD`.
4. Observe recurrence through real occurrence data and 30-day activation windows.

```powershell
errkb prevention add --issue <resolved-issue-id> --kind regression_test --title "Rename regression" --repo . --path tests/rename_test.go
errkb prevention verify <artifact-version-id> --repo . -- go test ./...
# Review and commit the artifact yourself before activation.
errkb prevention activate <artifact-version-id> --repo .
errkb prevention stats --issue <resolved-issue-id>
```

The four artifact kinds are `regression_test`, `deterministic_check`, `agent_instruction` and `intent`. MCP can register metadata and read preventions, but only the human-invoked CLI can verify or activate them. Web prevention statistics are read-only.

Activation records evidence; it does **not** install a CI job or host hook, edit a file or perform Git writes. See [`docs/prevention.md`](docs/prevention.md) for lifecycle rules, metrics, privacy and troubleshooting.

## Command overview

| Command                                                     | Purpose                                                  |
| ----------------------------------------------------------- | -------------------------------------------------------- |
| `errkb run -- <command>`                                    | Run a command, preserve its behavior and record failures |
| `errkb search <query>` / `show <id>`                        | Find and inspect known issues and fixes                  |
| `errkb record --event <file>`                               | Submit a complete event from a file or stdin             |
| `errkb list` / `stats`                                      | List issues or solutions and read aggregate metrics      |
| `errkb feedback <issue> <version>`                          | Record a solution outcome                                |
| `errkb prevention add\|verify\|activate\|list\|show\|stats` | Manage and observe prevention artifacts (**Unreleased**) |
| `errkb tail --file <path>`                                  | Capture failures from a growing log                      |
| `errkb ui --web`                                            | Open the local Web toolbox                               |
| `errkb mcp` / `mcp install` / `mcp selftest`                | Serve, register and verify MCP                           |
| `errkb db migrate\|status`                                  | Apply or inspect schema migrations                       |
| `errkb pg install\|start\|stop\|status\|uninstall`          | Manage the local PostgreSQL cluster                      |
| `errkb doctor`                                              | Run read-only environment, connector and MCP diagnostics |
| `errkb markdown import\|export`                             | Exchange the legacy `ERRORS.md` format                   |

Most read and write commands support `--json` with a versioned contract payload.

## Privacy and operating boundaries

- Knowledge stays on the local machine. The core makes no model calls, outbound network requests or telemetry reports.
- Redaction happens before database writes. HTTP is off by default; MCP uses stdio and the local API uses an owner-only Named Pipe.
- Prevention storage contains metadata, hashes and redacted bounded evidence, not artifact bodies or absolute repository paths.
- Retention functions and settings exist, but automatic scheduled purging is not currently wired into the daemon.
- The DSH adapter's offline queue can temporarily contain raw observations on local disk before core redaction.

Read [`docs/privacy.md`](docs/privacy.md) before storing sensitive project data.

## Current limitations

- PostgreSQL 16 is required; errkb is not a database-embedded single-file application.
- Releases are not code-signed; verify the published `SHA256SUMS`.
- Prevention activation does not automatically integrate an artifact into CI or a host workflow.
- errkb does not generate regression tests, edit prevention files, perform automatic Git writes or execute retrieved fixes.
- There is no team/cloud synchronization, vector search, private chain-of-thought capture or reliable hallucination detector.
- Generic log/manual host acceptance and the connector paths explicitly marked unverified in the matrix remain incomplete.

## Documentation

| Document                                                                                                                         | Purpose                                         |
| -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| [`docs/project-status.md`](docs/project-status.md) / [中文](docs/project-status.zh-CN.md)                                        | Release, source and verification status         |
| [`docs/install.md`](docs/install.md) / [中文](docs/install.zh-CN.md)                                                             | Install, upgrade, recovery and uninstall        |
| [`docs/features-and-implementation.md`](docs/features-and-implementation.md) / [中文](docs/features-and-implementation.zh-CN.md) | Feature-to-source implementation map            |
| [`docs/prevention.md`](docs/prevention.md) / [中文](docs/prevention.zh-CN.md)                                                    | Prevention workflow and metrics                 |
| [`docs/connector-matrix.md`](docs/connector-matrix.md) / [中文](docs/connector-matrix.zh-CN.md)                                  | Measured host capabilities and gaps             |
| [`docs/privacy.md`](docs/privacy.md) / [中文](docs/privacy.zh-CN.md)                                                             | Data storage, redaction, retention and deletion |
| [`docs/benchmarks.md`](docs/benchmarks.md) / [中文](docs/benchmarks.zh-CN.md)                                                    | Performance evidence and test environment       |
| [`CHANGELOG.md`](CHANGELOG.md)                                                                                                   | Release history and Unreleased changes          |

Historical design plans and acceptance records remain under `docs/`; they are evidence rather than current user entry points.

## Development

```powershell
pnpm install
pnpm typecheck
pnpm lint
pnpm format:check
pnpm test
pnpm build

go build ./...
go vet ./...
gofmt -l .
cue vet ./spec/...
```

Database integration tests require PostgreSQL and `ERRKB_TEST_DATABASE_URL`; without it they skip. Windows packaging scripts live in [`packaging/windows/`](packaging/windows).

Contributions should preserve the bilingual documentation and the existing contract, privacy and verification gates. See [`docs/implementation-plan.md`](docs/implementation-plan.md) for repository conventions.

## License

[MIT](LICENSE) © jingchangzhao-gif
