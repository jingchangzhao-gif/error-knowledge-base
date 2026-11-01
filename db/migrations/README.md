# Migrations

Schema changes live in this directory, one file per change. The runner is
`internal/database/migrate.go`; the entities are the ones from section 4 of the
implementation plan.

## Naming and order

- `NNNN_name.sql`, four-digit zero-padded version, lowercase name:
  `0002_outcome_links.sql`.
- Files run in filename order. Version 0001 is the initial schema; add 0002,
  0003, … and never reuse a number.
- One logical change per file. The runner wraps each file in its own
  transaction, so a file either fully applies or not at all.

## The ledger

The runner owns `schema_migrations`:

```
version      text primary key
name         text not null
checksum     text not null   -- SHA-256 of the raw file bytes
applied_at   timestamptz not null default now()
```

Rules:

- **Never edit a file that has been applied anywhere.** The runner compares the
  recorded checksum with the file on disk and fails with a checksum mismatch if
  they differ — even a comment edit. Add a new migration instead.
- A recorded version whose file disappeared is an error too: the schema on disk
  would no longer describe the database.
- Idempotency comes from the ledger, not from `IF NOT EXISTS` in the SQL. The
  ledger knows whether a file already ran, so a half-applied schema cannot be
  silently skipped, and a rerun applies nothing.
- Concurrent migrators serialize on a PostgreSQL advisory lock; a second
  process waits, then observes the ledger row the first one wrote.

`Migrate` returns the versions it applied; an unchanged directory returns an
empty slice.

## Adding a migration

1. Add `NNNN_name.sql` with plain SQL (no `BEGIN`/`COMMIT`; the runner supplies
   the transaction).
2. Apply it: start `errkb db migrate` (once the command exists) or run the
   runner through the tests below.
3. Never rewrite an applied file; corrections are new migrations.

## Rollback convention

Migrations are forward-only: there are no down files. To undo a change, write a
new migration that reverses it. A migration that was never applied outside a
disposable environment may be fixed in place — check the ledger first. In
development, dropping the schema and migrating again is the fast path.

## Was 0001 ever applied before EKB-019?

No. EKB-019 replaced the comment-only shell of `0001_init.sql` with the real
schema, and that is only safe because the file had never touched persistent
state:

- The local PostgreSQL 16.15 instance had **zero user tables** before the first
  real run, and no database carried a `schema_migrations` row for `0001`.
- The CI `database` job creates a fresh `postgres:16` service container for
  every run, applies every file once in that container, and throws the
  container away. Nothing persists between runs.

Measured before the fill (PostgreSQL 16.15, `postgres` on `127.0.0.1:5432`):

```
select count(*) from information_schema.tables
where table_schema = 'public' and table_type = 'BASE TABLE';   -- 0
```

## Tests

The migration tests are real PostgreSQL tests and are skipped when
`ERRKB_TEST_DATABASE_URL` is unset, so `go test ./...` works without a
database:

```
ERRKB_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  go test ./internal/database/ -v
```

Each test runs in a throwaway schema (`errkb_migrate_<nanoseconds>`) that is
dropped afterwards. Covered: fresh schema with exactly the fifteen business
tables (the original eleven plus the four prevention tables from 0009); second
run applies nothing and adds no ledger row; an edited file fails with a checksum
mismatch; a failing file rolls back both its statements and its ledger row; two
concurrent migrators apply the schema once; structural assertions (entity
separation, per-artifact retention, audit columns, no foreign keys on
`audit_log`, `events` columns aligned with `ErrorEvent v1`).

Migration 0009 adds immutable prevention artifact metadata, version and issue
links, explicit verification runs, recurrence indexes, and prevention-aware
scoped deletion. Upgrade tests load the committed 0007 schema dump and apply
0008 and 0009 directly with existing rows intact. A synthetic failing 0010
migration verifies transaction rollback without undoing the committed 0009
upgrade; unknown future ledger versions are rejected.

## CI

The `database` job applies every file in filename order to a fresh
`postgres:16` container with `psql -v ON_ERROR_STOP=1`, then opens 50 concurrent
sessions. The `go` job runs the unit tests; the migration tests are skipped
there because that job has no database service.
