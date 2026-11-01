# Privacy and data handling

[简体中文](privacy.zh-CN.md)

The Error Knowledge Base is built to keep a failure history on the machine that produced it. This page describes what is stored, where, for how long, and what is explicitly **not** done.

## Summary

- Nothing leaves the machine. The core never calls a model, makes no outbound network request, and has no telemetry or remote endpoint. Local PostgreSQL and an explicitly enabled loopback HTTP listener are still local transports.
- Redaction happens **before** anything is written to the knowledge base; the database only receives redacted text.
- The local API is a Named Pipe with an owner-only ACL; HTTP is off by default.
- Retention is explicit (three independent periods) and deletion is previewed and audited.
- The one place raw text can sit on disk is the DSH adapter's offline queue, which holds observations until they can be replayed — still local, still not sent anywhere.

## What is stored

| Data                                                                          | Where                                                                                                              | Notes                                                                                                                                       |
| ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Issue summary, fingerprint, category, project key, first/last seen, hit count | PostgreSQL `issues`                                                                                                | The fingerprint is a 12-hex hash of category + normalized text, not the text itself                                                         |
| Events (one row per recorded failure)                                         | PostgreSQL `events`                                                                                                | Carries the redacted sample and the normalized message; the raw output is not stored                                                        |
| Occurrences, environment                                                      | PostgreSQL `occurrences`                                                                                           | One row per replay or repeat, with the redacted event reference                                                                             |
| Solutions, versions, outcomes                                                 | PostgreSQL `solutions`, `solution_versions`, `outcomes`                                                            | Fix text must already be redacted; outcomes feed `errkb_trust()`                                                                            |
| Retrievals                                                                    | PostgreSQL `retrievals`                                                                                            | The query, the candidate, the match reason and the measured duration                                                                        |
| Notices                                                                       | PostgreSQL `notices`                                                                                               | What was shown (redacted), with an estimated token count                                                                                    |
| Audit trail                                                                   | PostgreSQL `audit_log`                                                                                             | Who changed what; append-only guards                                                                                                        |
| Prevention metadata (Unreleased)                                              | PostgreSQL `prevention_artifacts`, `prevention_artifact_versions`, `issue_preventions`, `prevention_verifications` | Repository-relative paths, hashes, Git commit IDs, redacted verification metadata/evidence; no artifact bodies or repository absolute paths |
| Cluster credentials                                                           | `%LOCALAPPDATA%\errkb\pg\credentials.json`                                                                         | Generated password, owner-only ACL                                                                                                          |
| Adapter offline queue                                                         | `state.json` next to the connector state                                                                           | Raw observations pending replay (see "Known gaps")                                                                                          |
| Daemon log                                                                    | `%LOCALAPPDATA%\errkb\logs\daemon.log`                                                                             | Redacted before writing; rotated by size                                                                                                    |

## Data flow

```text
host failure
   │
   ▼
adapter (hook / wrapper / MCP)
   │  ErrorEvent v1 fields, redaction rules applied by the core
   ▼
core pipeline: redact → normalize → fingerprint → validate
   │
   ▼
PostgreSQL (local)  ◀── retention, deletion, audit
   │
   ▼
retrieval → fix + match reason + trust → back to the host
```

Redaction is one rule source, `spec/redact.cue`. It generates the Go engine
(`internal/redact/`), the TypeScript adapter patterns (`src/redact-patterns.ts`) and the CI
privacy guard, so the three cannot drift. Rules cover API keys and tokens (`sk-`, bearer
tokens, cloud keys, GitHub tokens), passwords in connection strings, e-mail addresses,
request ids, hexadecimal and base64 blobs, absolute home directories and absolute paths;
placeholders such as `<secret>`, `<email>`, `<request-id>`, `<hex>`, `<base64>` and
`<path>` take their place.

The contract rejects payloads that still contain sensitive shapes: an `ErrorEvent` that
still looks like it carries a key, a home path or a request id is refused before it reaches
the database. `errkb doctor` includes a privacy-pattern check that reports whether the
running rules still detect the known shapes.

## Prevention artifacts and verification (Unreleased)

The CLI hashes a regular repository file of at most **1 MiB**; absolute paths,
symbolic links, `.git` traversal and repository escapes are rejected. Only the
repository-relative path and SHA-256 are persisted, never the artifact body or
repository absolute path. Activation stores the clean tracked Git commit ID, not
repository contents.

Verification receives an explicit argv array, not an implicit shell command.
Command arguments and bounded stdout/stderr evidence are redacted before storage.
The environment object is a fixed allowlist (`os`, `shell`, `go_version`,
`git_version`); errkb does not dump process environment variables. MCP
`err_prevention_register` accepts caller-supplied metadata and a hash without
reading the file, so its response is not proof of file content. Verification and
activation remain human CLI decisions. Backups include the stored prevention
metadata and redacted evidence. See [prevention.md](prevention.md).

## Transport

- **Named Pipe (default).** `\\.\pipe\errkb`, owner-only ACL (`D:P(A;;GA;;;<user>)(A;;GA;;;SY)`). No TCP listener is opened unless HTTP is enabled.
- **HTTP (off by default).** When enabled it must bind a loopback address (`127.0.0.1`, `::1`, `localhost`) and requires a token of at least 32 characters; the configuration is rejected otherwise.
- **MCP** runs on stdio, inside the process that starts it. It is not exposed over the network.
- **Adapters** talk to the daemon through the pipe (MCP tools from Codex/Claude use the same pipe when the server is configured with the errkb executable).

## Retention

The knowledge base has three independent retention classes:

| Class             | What it removes                                                                                | Default  |
| ----------------- | ---------------------------------------------------------------------------------------------- | -------- |
| `sample_days`     | The redacted sample text on old events, while keeping the event row and its normalized message | 7 days   |
| `normalized_days` | Whole events older than this                                                                   | 90 days  |
| `audit_days`      | Audit rows                                                                                     | 365 days |

The periods are configured in `spec/config.cue` / the config file and applied by
`errkb_purge_retention()` (see `db/migrations/0006_retention.sql`). The function validates
non-negative periods and returns what it removed per class.

**Current limitation:** the daemon does not run retention on a schedule yet; a pass is
triggered explicitly (the settings-page model and the database function exist). Until
scheduling lands, data stays longer than the configured period unless an operator runs the
pass.

## Deletion

- Scoped deletion (by project, connector and/or before a timestamp) is available through
  `errkb_delete_scope`: it previews the row counts (`errkb_delete_preview`), then deletes
  issues, solutions, versions, outcomes, occurrences, retrievals, notices, events and the
  applicable prevention chain in one transaction. A preview makes the impact visible before
  anything changes. Connector-only, time-only and retention passes preserve active prevention
  artifacts; project deletion removes that project's artifacts and all of their issue links.
- The deletion writes an `audit_log` row (`delete.scope`) with the counts and the operator.
- `audit_log` is append-only: direct `DELETE` or `TRUNCATE` is blocked by triggers; only the
  retention path may remove audit rows.
- Uninstalling asks keep-or-delete for the PostgreSQL cluster. **Delete data** removes the
  cluster directory and credentials; **keep data** leaves them for a reinstall.

## What never happens

- No model inference, no prompt or completion call, and no embedding in the core runtime
  path. The known fix is returned from the local database, not generated.
- No network requests from the core. The only network-ish interfaces are the local
  PostgreSQL connection and, if the operator enables it, a loopback HTTP listener.
- No account, telemetry, analytics or crash reporting. The daemon log is local and
  redacted.
- No raw command output in the knowledge base: only the redacted sample, the normalized
  message and the structured fields.

## Known gaps

- **DSH adapter offline queue.** The adapter keeps raw observations on disk until the core
  is reachable (`state.json` next to the connector state). They are redacted when they
  reach the core, before any database write, but until then the file holds the raw text.
  It never leaves the machine.
- **Retention is manual.** See the limitation above.
- **Backups copy everything.** A `pg_dump` includes the same data the database holds
  (redacted, but complete). Protect the dump file like the database.
- **The release is unsigned.** Integrity is protected by the release `SHA256SUMS`, not by
  a signature.

## User controls

- Config file: capture on/off, global and per-connector/project pause, three retention
  periods, deletion, HTTP off/on with token, log and health paths.
- Settings page model: the same controls, with a preview before deletion and a confirmation
  before destructive actions.
- Uninstall: keep or delete the data.
- Delete rows through the scoped deletion API; individual audit rows are reserved for the
  retention pass.
