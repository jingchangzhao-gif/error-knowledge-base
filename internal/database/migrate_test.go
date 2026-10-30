package database_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

const testDatabaseEnv = "ERRKB_TEST_DATABASE_URL"

// migrationsDir is the directory the binary ships with, relative to this
// package's test working directory.
func migrationsDir() string {
	return filepath.Join("..", "..", "db", "migrations")
}

// connect opens the database named by ERRKB_TEST_DATABASE_URL. The migration
// tests are real PostgreSQL tests: without the variable they are skipped so the
// default `go test ./...` stays runnable on a machine without a database.
func connect(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv(testDatabaseEnv)
	if url == "" {
		t.Skipf("%s is not set; set it to a PostgreSQL 16 database URL to run the migration tests", testDatabaseEnv)
	}
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to %s: %v", testDatabaseEnv, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// freshSchema gives the connection a private schema so tests never touch the
// public schema of the development database.
func freshSchema(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("errkb_migrate_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create schema %s: %v", schema, err)
	}
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("set search_path to %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "drop schema if exists "+schema+" cascade")
	})
	return schema
}

// migrate runs the real migration directory and fails the test on error.
func migrate(t *testing.T, conn *pgx.Conn, dir string) []string {
	t.Helper()
	applied, err := database.Migrate(context.Background(), conn, dir)
	if err != nil {
		t.Fatalf("migrate %s: %v", dir, err)
	}
	return applied
}

func ledgerRows(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), "select count(*) from "+database.LedgerTable).Scan(&count); err != nil {
		t.Fatalf("count ledger rows: %v", err)
	}
	return count
}

func tableNames(t *testing.T, conn *pgx.Conn) map[string]bool {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		"select table_name from information_schema.tables where table_schema = current_schema() and table_type = 'BASE TABLE'")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	names := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	return names
}

func columnNames(t *testing.T, conn *pgx.Conn, table string) map[string]bool {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		"select column_name from information_schema.columns where table_schema = current_schema() and table_name = $1", table)
	if err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list columns of %s: %v", table, err)
	}
	return columns
}

func writeMigration(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestMigrateCreatesSchemaV1 applies the real migration directory to an empty
// schema and checks that every entity of section 4 exists and nothing else.
func TestMigrateCreatesSchemaV1(t *testing.T) {
	conn := connect(t)
	freshSchema(t, conn)

	expected, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the migration files: %v", err)
	}
	applied := migrate(t, conn, migrationsDir())
	if len(applied) != len(expected) {
		t.Fatalf("first run applied %v, want the %d files %v", applied, len(expected), expected)
	}
	for i, migration := range expected {
		if applied[i] != migration.Version {
			t.Fatalf("first run applied %v, want the file order", applied)
		}
	}

	want := []string{
		"audit_log", "connectors", "events", "issues", "notices", "occurrences",
		"outcomes", "retrievals", "sessions", "solution_versions", "solutions",
		"prevention_artifacts", "prevention_artifact_versions", "issue_preventions", "prevention_verifications",
	}
	got := tableNames(t, conn)
	if len(got) != len(want)+1 { // + schema_migrations
		t.Fatalf("schema has %d tables (%v), want %d business tables plus the ledger", len(got), got, len(want))
	}
	for _, table := range want {
		if !got[table] {
			t.Errorf("table %s is missing, got %v", table, got)
		}
	}
	if !got[database.LedgerTable] {
		t.Errorf("ledger table %s is missing, got %v", database.LedgerTable, got)
	}
}

// TestMigrateIsIdempotent runs the same directory twice: the second run must
// apply nothing and must not duplicate ledger rows.
func TestMigrateIsIdempotent(t *testing.T) {
	conn := connect(t)
	freshSchema(t, conn)

	expected, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the migration files: %v", err)
	}
	migrate(t, conn, migrationsDir())
	second := migrate(t, conn, migrationsDir())
	if len(second) != 0 {
		t.Fatalf("second run applied %v, want nothing", second)
	}
	if rows := ledgerRows(t, conn); rows != len(expected) {
		t.Fatalf("ledger has %d rows after two runs, want %d", rows, len(expected))
	}
}

// TestMigrateRejectsEditedMigration proves the checksum protects files that are
// already recorded: editing comment text is enough to be rejected.
func TestMigrateRejectsEditedMigration(t *testing.T) {
	conn := connect(t)
	freshSchema(t, conn)

	dir := t.TempDir()
	path := writeMigration(t, dir, "0001_init.sql", "create table scratch (id integer primary key);\n")
	migrate(t, conn, dir)

	edited, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(edited, []byte("-- edited after it was applied\n")...), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}

	_, err = database.Migrate(context.Background(), conn, dir)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("second run error = %v, want a checksum mismatch", err)
	}
	if rows := ledgerRows(t, conn); rows != 1 {
		t.Fatalf("ledger has %d rows after the rejected run, want 1", rows)
	}
}

// TestMigrateRollsBackFailedMigration fails the second statement of a file and
// checks that neither the first statement nor the ledger row survived.
func TestMigrateRollsBackFailedMigration(t *testing.T) {
	conn := connect(t)
	freshSchema(t, conn)

	dir := t.TempDir()
	writeMigration(t, dir, "0001_init.sql", strings.Join([]string{
		"create table scratch_ok (id integer primary key);",
		"create table scratch_ok (id integer primary key);", // duplicate: fails
	}, "\n"))

	_, err := database.Migrate(context.Background(), conn, dir)
	if err == nil {
		t.Fatal("migrate succeeded, want the duplicate-table error")
	}
	if got := tableNames(t, conn); got["scratch_ok"] {
		t.Fatalf("scratch_ok survived a failed migration, got %v", got)
	}
	if rows := ledgerRows(t, conn); rows != 0 {
		t.Fatalf("ledger has %d rows after a failed migration, want 0", rows)
	}
}

// TestMigrateConcurrent starts two migrators on separate connections: one
// applies, the other waits on the advisory lock and then applies nothing, and
// the ledger never gains a duplicate row.
func TestMigrateConcurrent(t *testing.T) {
	first := connect(t)
	schema := freshSchema(t, first)

	second := connect(t)
	if _, err := second.Exec(context.Background(), "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("set search_path on the second connection: %v", err)
	}

	type result struct {
		applied []string
		err     error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, conn := range []*pgx.Conn{first, second} {
		wg.Add(1)
		go func(conn *pgx.Conn) {
			defer wg.Done()
			<-start
			applied, err := database.Migrate(context.Background(), conn, migrationsDir())
			results <- result{applied: applied, err: err}
		}(conn)
	}
	close(start)
	wg.Wait()
	close(results)

	expected, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the migration files: %v", err)
	}
	total := 0
	for outcome := range results {
		if outcome.err != nil {
			t.Fatalf("concurrent migrate: %v", outcome.err)
		}
		total += len(outcome.applied)
	}
	if total != len(expected) {
		t.Fatalf("the two migrators applied %d versions in total, want exactly %d", total, len(expected))
	}
	if rows := ledgerRows(t, first); rows != len(expected) {
		t.Fatalf("ledger has %d rows after a concurrent run, want %d", rows, len(expected))
	}
}

// TestSchemaStructure locks the structural promises of section 4: separated
// entities, per-artifact retention, audit columns, no cascade into audit_log,
// and events columns that match the ErrorEvent v1 contract.
func TestSchemaStructure(t *testing.T) {
	conn := connect(t)
	freshSchema(t, conn)
	migrate(t, conn, migrationsDir())

	eventFields := []string{
		"event_id", "connector_id", "session_id", "observed_at", "channel",
		"category", "severity", "transient", "headline", "normalized_message",
		"redacted_sample", "error_code", "command", "exit_code", "tool_name",
		"os", "shell", "runtime_versions", "project_key", "fingerprint",
		"schema_version", "evidence", "confidence",
	}
	events := columnNames(t, conn, "events")
	for _, field := range eventFields {
		if !events[field] {
			t.Errorf("events is missing contract field %s", field)
		}
	}
	if events["issue_id"] {
		t.Error("events must not carry issue_id: an occurrence is not automatically an issue")
	}

	for table, columns := range map[string][]string{
		"events":    {"sample_retain_until", "normalized_retain_until"},
		"audit_log": {"retain_until"},
	} {
		present := columnNames(t, conn, table)
		for _, column := range columns {
			if !present[column] {
				t.Errorf("%s is missing the retention column %s", table, column)
			}
		}
	}

	for _, table := range []string{"connectors", "sessions", "issues", "solutions"} {
		columns := columnNames(t, conn, table)
		for _, field := range []string{"created_at", "updated_at", "source", "actor"} {
			if !columns[field] {
				t.Errorf("%s is missing the audit field %s", table, field)
			}
		}
	}

	var auditForeignKeys int
	if err := conn.QueryRow(context.Background(), `
		select count(*) from pg_constraint
		where contype = 'f'
		  and (conrelid = 'audit_log'::regclass or confrelid = 'audit_log'::regclass)
	`).Scan(&auditForeignKeys); err != nil {
		t.Fatalf("inspect audit_log foreign keys: %v", err)
	}
	if auditForeignKeys != 0 {
		t.Fatalf("audit_log has %d foreign keys, want 0 so business deletes cannot cascade into it", auditForeignKeys)
	}

	preventionFields := map[string][]string{
		"prevention_artifacts":         {"id", "project_key", "kind", "title", "created_at", "updated_at", "source", "actor"},
		"prevention_artifact_versions": {"id", "artifact_id", "version", "artifact_path", "content_sha256", "status", "commit_sha", "activated_at", "created_at", "source", "actor"},
		"issue_preventions":            {"issue_id", "artifact_id", "created_at", "source", "actor"},
		"prevention_verifications":     {"id", "artifact_version_id", "command", "exit_code", "passed", "duration_ms", "evidence", "environment", "content_sha256", "created_at", "source", "actor"},
	}
	for table, fields := range preventionFields {
		columns := columnNames(t, conn, table)
		for _, field := range fields {
			if !columns[field] {
				t.Errorf("%s is missing %s", table, field)
			}
		}
	}
	rows, err := conn.Query(context.Background(), `
		select c.conrelid::regclass::text, pg_get_constraintdef(c.oid)
		from pg_constraint c
		where c.conrelid::regclass::text in ('prevention_artifacts', 'prevention_artifact_versions', 'prevention_verifications')`)
	if err != nil {
		t.Fatalf("list prevention constraints: %v", err)
	}
	defer rows.Close()
	var constraints string
	for rows.Next() {
		var table, definition string
		if err := rows.Scan(&table, &definition); err != nil {
			t.Fatal(err)
		}
		constraints += table + " " + definition + "\n"
	}
	for _, fragment := range []string{"regression_test", "deterministic_check", "agent_instruction", "draft", "verified", "active", "deprecated", "[0-9a-f]", "duration_ms >= 0", "status = 'active'", "version >= 1"} {
		if !strings.Contains(constraints, fragment) {
			t.Errorf("prevention constraints do not contain %q:\n%s", fragment, constraints)
		}
	}
	var activeIndex bool
	if err := conn.QueryRow(context.Background(), `select exists(select 1 from pg_indexes where schemaname = current_schema() and indexname = 'prevention_one_active_version' and indexdef like '%WHERE (status = ' || quote_literal('active') || '%')`).Scan(&activeIndex); err != nil {
		t.Fatalf("inspect active index: %v", err)
	}
	if !activeIndex {
		t.Error("prevention_one_active_version partial unique index is missing")
	}
	var immutableTrigger bool
	if err := conn.QueryRow(context.Background(), `select exists(select 1 from pg_trigger where tgrelid = 'prevention_artifact_versions'::regclass and tgname = 'prevention_version_immutable' and not tgisinternal)`).Scan(&immutableTrigger); err != nil {
		t.Fatalf("inspect immutable trigger: %v", err)
	}
	if !immutableTrigger {
		t.Error("prevention_version_immutable trigger is missing")
	}
	for _, index := range []string{"issue_preventions_artifact_idx", "prevention_versions_artifact_idx", "prevention_verifications_version_created_idx", "prevention_versions_activated_idx", "occurrences_issue_occurred_idx"} {
		var exists bool
		if err := conn.QueryRow(context.Background(), `select exists(select 1 from pg_indexes where schemaname = current_schema() and indexname = $1)`, index).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("index %s is missing", index)
		}
	}
}

func TestPreventionDeletionPreviewMatchesExecution(t *testing.T) {
	ctx := context.Background()
	conn := connect(t)
	freshSchema(t, conn)
	migrate(t, conn, migrationsDir())
	var issueID, otherIssueID, artifactID, versionID string
	if err := conn.QueryRow(ctx, `insert into issues (fingerprint, project_key, title, category, source, actor) values ('aaaaaaaaaaaa', 'alpha', 'a', 'tool', 'test', 'owner') returning id`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `insert into issues (fingerprint, project_key, title, category, source, actor) values ('bbbbbbbbbbbb', 'beta', 'b', 'tool', 'test', 'owner') returning id`).Scan(&otherIssueID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `insert into prevention_artifacts (project_key, kind, title, source, actor) values ('alpha', 'intent', 'prevent a', 'test', 'owner') returning id`).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `insert into prevention_artifact_versions (artifact_id, version, artifact_path, content_sha256, status, commit_sha, activated_at, source, actor) values ($1, 1, 'intent.md', $2, 'active', $3, now(), 'test', 'owner') returning id`, artifactID, strings.Repeat("a", 64), strings.Repeat("b", 40)).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{issueID, otherIssueID} {
		if _, err := conn.Exec(ctx, `insert into issue_preventions (issue_id, artifact_id, source, actor) values ($1, $2, 'test', 'owner')`, id, artifactID); err != nil {
			t.Fatal(err)
		}
	}
	// A target issue may point at another project's artifact. The artifact stays,
	// but this link is deleted by the issue cascade and must be previewed/count audited.
	var otherArtifactID string
	if err := conn.QueryRow(ctx, `insert into prevention_artifacts (project_key, kind, title, source, actor) values ('beta', 'intent', 'prevent b', 'test', 'owner') returning id`).Scan(&otherArtifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into issue_preventions (issue_id, artifact_id, source, actor) values ($1, $2, 'test', 'owner')`, issueID, otherArtifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into prevention_verifications (artifact_version_id, command, passed, duration_ms, evidence, content_sha256, source, actor) values ($1, '["go","test"]', true, 1, 'ok', $2, 'test', 'owner')`, versionID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	preview := map[string]int64{}
	rows, err := conn.Query(ctx, `select kind, rows from errkb_delete_preview('alpha', null, null)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		preview[kind] = count
	}
	rows.Close()
	for kind, want := range map[string]int64{"prevention_verifications": 1, "issue_preventions": 3, "prevention_artifact_versions": 1, "prevention_artifacts": 1} {
		if preview[kind] != want {
			t.Errorf("preview %s = %d, want %d", kind, preview[kind], want)
		}
	}
	executed := map[string]int64{}
	rows, err = conn.Query(ctx, `select kind, removed from errkb_delete_scope('alpha', null, null, 'owner')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		executed[kind] = count
	}
	rows.Close()
	for kind, want := range map[string]int64{"prevention_verifications": 1, "issue_preventions": 3, "prevention_artifact_versions": 1, "prevention_artifacts": 1} {
		if executed[kind] != want {
			t.Errorf("executed %s = %d, want %d", kind, executed[kind], want)
		}
	}
	var auditCount, otherLinks int
	if err := conn.QueryRow(ctx, `select count(*) from audit_log where action = 'delete.scope'
		and detail->'counts'->>'prevention_verifications' = '1'
		and detail->'counts'->>'issue_preventions' = '3'
		and detail->'counts'->>'prevention_artifact_versions' = '1'
		and detail->'counts'->>'prevention_artifacts' = '1'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `select count(*) from issue_preventions where issue_id = $1`, otherIssueID).Scan(&otherLinks); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 || otherLinks != 0 {
		t.Fatalf("audit count = %d, remaining cross-issue links = %d; want 1 and 0", auditCount, otherLinks)
	}
}
