package database_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// upgradeFixtures holds real artifacts written by earlier releases. Every file
// is committed, so the upgrade paths stay testable without a machine created in
// the past. See spec/compat.cue's `upgrade` block for the promise each fixture
// guards and where it came from.
const upgradeFixtures = "../../db/migrations/testdata/upgrade"

func readUpgradeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(upgradeFixtures, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// upgradeConnection opens the test database and loads the real schema dump of a
// database migrated to 0007 into a private schema. The ledger rows in the dump
// make Migrate treat 0001..0007 as already applied.
func upgradeConnection(t *testing.T) *pgx.Conn {
	t.Helper()
	conn := connect(t)
	freshSchema(t, conn)
	if _, err := conn.Exec(context.Background(), string(readUpgradeFixture(t, "schema-0007.sql")), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("load schema-0007.sql: %v", err)
	}
	return conn
}

func columnExists(t *testing.T, conn *pgx.Conn, table, column string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(context.Background(), `
		select exists (
			select 1 from information_schema.columns
			where table_schema = current_schema() and table_name = $1 and column_name = $2
		)`, table, column).Scan(&exists)
	if err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	return exists
}

func tableExists(t *testing.T, conn *pgx.Conn, table string) bool {
	t.Helper()
	var exists bool
	err := conn.QueryRow(context.Background(), `
		select exists (
			select 1 from information_schema.tables
			where table_schema = current_schema() and table_name = $1
		)`, table).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}

func ledgerVersions(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), "select version from schema_migrations order by version")
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan a ledger row: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	return versions
}

// upgradeTables are every table the schema owns; the counts must not move across
// a forward migration.
var upgradeTables = []string{
	"connectors",
	"sessions",
	"events",
	"issues",
	"occurrences",
	"solutions",
	"solution_versions",
	"outcomes",
	"retrievals",
	"notices",
	"audit_log",
}

func tableCounts(t *testing.T, conn *pgx.Conn) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(upgradeTables))
	for _, table := range upgradeTables {
		counts[table] = rowCount(t, conn, table)
	}
	return counts
}

// TestUpgradeFrom0007KeepsTheData proves the promise in spec/compat.cue: a
// database that holds real rows at migration 0007 moves to 0008 with every row
// intact, and the one new column is nullable so old rows stay valid.
func TestUpgradeFrom0007KeepsTheData(t *testing.T) {
	ctx := context.Background()
	conn := upgradeConnection(t)

	if columnExists(t, conn, "retrievals", "duration_ms") {
		t.Fatal("the loaded dump already has duration_ms; it is not a 0007 schema")
	}
	if got, want := ledgerVersions(t, conn), []string{"0001", "0002", "0003", "0004", "0005", "0006", "0007"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger before the upgrade = %v, want %v", got, want)
	}

	connectorID := insertTestConnector(t, conn)
	sessionID := insertTestSession(t, conn, connectorID)
	event := recordFixture(connectorID, sessionID, eventUUID(1), "pnpm build: ERR_PNPM_NO_SCRIPT Missing script: build", "upgrade-project")
	recorded, err := database.RecordEvent(ctx, conn, event)
	if err != nil {
		t.Fatalf("record an event in the old schema: %v", err)
	}
	solutionID, err := database.CreateSolution(ctx, conn, recorded.IssueID, "upgrade-test", "owner")
	if err != nil {
		t.Fatalf("create a solution: %v", err)
	}
	version, err := database.AddSolutionVersion(ctx, conn, solutionID, "Add the missing build script.", "", "", database.Environment{}, "upgrade-test", "owner")
	if err != nil {
		t.Fatalf("add a solution version: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err != nil {
		t.Fatalf("verify the solution: %v", err)
	}
	if _, err := database.RecordOutcome(ctx, conn, recorded.IssueID, version.ID, database.OutcomeSuccess, "", database.Environment{}, "", "owner", "owner"); err != nil {
		t.Fatalf("record an outcome: %v", err)
	}
	// A retrieval row exactly as the 0007 schema records it (no duration_ms yet).
	if _, err := conn.Exec(ctx, `
		insert into retrievals (session_id, query, candidate_issue_id, candidate_rank, score, match_fields, approximate, environment_diff)
		values ($1, 'pnpm build', $2, 1, 0.9, '{fingerprint}', false, '[]'::jsonb)`,
		sessionID, recorded.IssueID); err != nil {
		t.Fatalf("insert a pre-upgrade retrieval row: %v", err)
	}

	before := tableCounts(t, conn)
	applied := migrate(t, conn, migrationsDir())
	if !reflect.DeepEqual(applied, []string{"0008", "0009"}) {
		t.Fatalf("applied = %v, want 0008 and 0009", applied)
	}
	if !columnExists(t, conn, "retrievals", "duration_ms") {
		t.Fatal("0008 did not add retrievals.duration_ms")
	}
	if after := tableCounts(t, conn); !reflect.DeepEqual(before, after) {
		t.Fatalf("row counts changed across the upgrade; before %v, after %v", before, after)
	}
	for _, table := range []string{"prevention_artifacts", "prevention_artifact_versions", "issue_preventions", "prevention_verifications"} {
		if got := rowCount(t, conn, table); got != 0 {
			t.Fatalf("%s has %d rows after upgrade, want 0", table, got)
		}
	}
	if got := ledgerVersions(t, conn); len(got) != 9 || got[8] != "0009" {
		t.Fatalf("ledger after the upgrade = %v, want 0001..0009", got)
	}
	if got := storedHeadline(t, conn, event.EventID); got != event.Headline {
		t.Fatalf("stored headline = %q, want %q", got, event.Headline)
	}

	// Old retrieval rows have no measurement, so the percentile would be wrong if
	// the column had a default: it must stay null and be excluded, not read as 0.
	var duration *int
	if err := conn.QueryRow(ctx, "select duration_ms from retrievals where candidate_issue_id = $1", recorded.IssueID).Scan(&duration); err != nil {
		t.Fatalf("read the upgraded retrieval row: %v", err)
	}
	if duration != nil {
		t.Fatalf("old retrieval row got duration_ms = %d, want null", *duration)
	}

	// The new code runs on the upgraded schema: a retrieval writes the new
	// duration column, and a fresh event still records.
	result, err := database.Retrieve(ctx, conn, database.RetrievalRequest{Fingerprint: event.Fingerprint, ProjectKey: "upgrade-project"})
	if err != nil {
		t.Fatalf("retrieve after the upgrade: %v", err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("the upgraded database returned no candidate for the recorded fingerprint")
	}
	var unmeasured, measured int
	if err := conn.QueryRow(ctx, "select count(*) filter (where duration_ms is null), count(*) filter (where duration_ms is not null) from retrievals").Scan(&unmeasured, &measured); err != nil {
		t.Fatalf("count retrieval durations: %v", err)
	}
	if unmeasured != 1 || measured != 1 {
		t.Fatalf("retrievals = %d unmeasured, %d measured; want 1 and 1", unmeasured, measured)
	}
	fresh := recordFixture(connectorID, sessionID, eventUUID(2), "npm ERR! code E404 package not found", "upgrade-project")
	recorded, err = database.RecordEvent(ctx, conn, fresh)
	if err != nil {
		t.Fatalf("record a new event after the upgrade: %v", err)
	}
	if !recorded.Created {
		t.Fatal("the new event did not create an issue on the upgraded schema")
	}
}

// TestUpgradeRejectsANewerLedgerVersion proves the forward-only rule: a
// database migrated by a newer release must not be opened by this binary, even
// though its tables happen to look compatible. Guessing would corrupt data.
func TestUpgradeRejectsANewerLedgerVersion(t *testing.T) {
	ctx := context.Background()
	conn := upgradeConnection(t)
	migrate(t, conn, migrationsDir())
	if _, err := conn.Exec(ctx, "insert into schema_migrations (version, name, checksum) values ('9999', 'from-a-newer-release', 'unknown-to-this-binary')"); err != nil {
		t.Fatalf("insert a newer ledger row: %v", err)
	}
	if _, err := database.Migrate(ctx, conn, migrationsDir()); err == nil {
		t.Fatal("a ledger row from a newer release was accepted")
	} else if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("error %q does not name the unknown ledger version", err)
	}
}

// TestUpgradeRollsBackAFailedMigration proves the other half of the promise: a
// migration that fails leaves the database exactly as it was before that
// migration started, so an operator can fix the file and re-run instead of
// restoring a backup.
func TestUpgradeRollsBackAFailedMigration(t *testing.T) {
	ctx := context.Background()
	conn := upgradeConnection(t)
	connectorID := insertTestConnector(t, conn)
	sessionID := insertTestSession(t, conn, connectorID)
	event := recordFixture(connectorID, sessionID, eventUUID(2), "ERESOLVE unable to resolve dependency tree", "rollback-project")
	if _, err := database.RecordEvent(ctx, conn, event); err != nil {
		t.Fatalf("record an event: %v", err)
	}
	before := tableCounts(t, conn)

	dir := t.TempDir()
	entries, err := os.ReadDir(migrationsDir())
	if err != nil {
		t.Fatalf("read the migration directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(migrationsDir(), entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", entry.Name(), err)
		}
	}
	broken := "0010_broken.sql"
	brokenBody := "create table broken_probe (id integer);\ndo $$ begin raise exception 'the upgrade fails on purpose'; end $$;\n"
	if err := os.WriteFile(filepath.Join(dir, broken), []byte(brokenBody), 0o600); err != nil {
		t.Fatalf("write the failing migration: %v", err)
	}

	_, err = database.Migrate(ctx, conn, dir)
	if err == nil {
		t.Fatal("the failing migration was reported as successful")
	}
	if !strings.Contains(err.Error(), "0010") {
		t.Fatalf("error %q does not name the failing migration", err)
	}
	if tableExists(t, conn, "broken_probe") {
		t.Fatal("a table created by the failed migration survived; the transaction was not rolled back")
	}
	if !tableExists(t, conn, "prevention_artifacts") {
		t.Fatal("successful 0009 was rolled back with failed 0010")
	}
	if got := ledgerVersions(t, conn); len(got) != 9 || got[8] != "0009" {
		t.Fatalf("ledger after the failed migration = %v, want 0001..0009", got)
	}
	if after := tableCounts(t, conn); !reflect.DeepEqual(before, after) {
		t.Fatalf("row counts changed across the failed migration; before %v, after %v", before, after)
	}

	// Removing the broken file and re-running must apply nothing: 0008 and 0009
	// are recorded exactly once and the retry does not double-apply them.
	if err := os.Remove(filepath.Join(dir, broken)); err != nil {
		t.Fatalf("remove the failing migration: %v", err)
	}
	if applied := migrate(t, conn, dir); len(applied) != 0 {
		t.Fatalf("retry applied %v, want nothing", applied)
	}
	if got := ledgerVersions(t, conn); len(got) != 9 {
		t.Fatalf("ledger after the retry = %v, want 9 rows", got)
	}
}

// TestOldConfigurationStillLoads proves an old configuration file keeps working
// after optional blocks were added, and that an unknown key is still rejected
// instead of being ignored.
func TestOldConfigurationStillLoads(t *testing.T) {
	raw := readUpgradeFixture(t, "config-v0.yaml")
	cfg, err := contract.LoadConfig(raw)
	if err != nil {
		t.Fatalf("load the old configuration: %v", err)
	}
	if got, want := cfg.Database.DSN, "postgres://errkb@127.0.0.1:5433/errkb?sslmode=disable"; got != want {
		t.Fatalf("dsn = %q, want %q", got, want)
	}
	if got := cfg.Service.Pipe.Name; got != "errkb" {
		t.Fatalf("pipe.name = %q, want errkb", got)
	}
	if cfg.Service.Pipe.Enabled == nil || !*cfg.Service.Pipe.Enabled {
		t.Fatal("pipe.enabled = nil or false; the default did not survive")
	}
	if cfg.Service.HTTP.Enabled == nil || *cfg.Service.HTTP.Enabled {
		t.Fatal("http.enabled should still be false")
	}
	if got := cfg.Capture.PausedProjects; !reflect.DeepEqual(got, []string{"legacy-project"}) {
		t.Fatalf("paused_projects = %v, want [legacy-project]", got)
	}
	if got := cfg.Capture.PausedConnectors; !reflect.DeepEqual(got, []string{"legacy-connector"}) {
		t.Fatalf("paused_connectors = %v, want [legacy-connector]", got)
	}
	if cfg.Notices.BeforeDiagnosis == nil || !*cfg.Notices.BeforeDiagnosis {
		t.Fatal("notices.before_diagnosis did not survive")
	}
	if cfg.Retention.SampleDays == nil || *cfg.Retention.SampleDays != 7 ||
		cfg.Retention.NormalizedDays == nil || *cfg.Retention.NormalizedDays != 30 ||
		cfg.Retention.AuditDays == nil || *cfg.Retention.AuditDays != 365 {
		t.Fatal("the three retention periods did not survive")
	}
	if got := cfg.Redaction.ExtraPatterns; !reflect.DeepEqual(got, []string{"corp-id-[0-9]{6}"}) {
		t.Fatalf("extra_patterns = %v", got)
	}
	if cfg.Search.Limit == nil || *cfg.Search.Limit != 3 {
		t.Fatal("search.limit did not survive")
	}
	if cfg.Timeout.CaptureMS == nil || *cfg.Timeout.CaptureMS != 2000 ||
		cfg.Timeout.SearchMS == nil || *cfg.Timeout.SearchMS != 1500 ||
		cfg.Timeout.MCPMS == nil || *cfg.Timeout.MCPMS != 5000 {
		t.Fatal("timeouts did not survive")
	}
	if cfg.Offline.ReplayIntervalMS == nil || *cfg.Offline.ReplayIntervalMS != 5000 {
		t.Fatal("offline.replay_interval_ms did not survive")
	}
	// The optional block added after this file was written stays unset.
	if cfg.Templates != nil {
		t.Fatalf("templates = %+v, want nil for a pre-templates file", cfg.Templates)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the old configuration no longer validates: %v", err)
	}

	// An unknown key is a typo or a file from a newer release: it must fail
	// loudly, not be dropped on the floor.
	if _, err := contract.LoadConfig(append(append([]byte{}, raw...), []byte("future_option: true\n")...)); err == nil {
		t.Fatal("an unknown configuration key was accepted")
	} else if !strings.Contains(err.Error(), "future_option") {
		t.Fatalf("error %q does not name the unknown key", err)
	}
}

// TestOldMarkdownUpgradesWithHistory proves the Markdown exchange format keeps
// working: the TypeScript-era seed imports without loss, and a document with two
// solution versions keeps both, newest first, exactly once.
func TestOldMarkdownUpgradesWithHistory(t *testing.T) {
	ctx := context.Background()
	conn := connect(t)
	freshSchema(t, conn)
	migrate(t, conn, migrationsDir())

	seed := string(readUpgradeFixture(t, "ERRORS-v0.md"))
	report, err := database.ImportMarkdown(ctx, conn, seed)
	if err != nil {
		t.Fatalf("import the TypeScript-era document: %v", err)
	}
	if report.Entries != 3 || report.IssuesCreated != 3 || report.VersionsAdded != 3 {
		t.Fatalf("import report = %+v, want 3 entries, 3 issues, 3 versions", report)
	}
	if got := rowCount(t, conn, "issues"); got != 3 {
		t.Fatalf("issues after the import = %d, want 3", got)
	}

	exported, err := database.ExportMarkdown(ctx, conn)
	if err != nil {
		t.Fatalf("export after the import: %v", err)
	}
	parsed, err := database.ParseMarkdown(exported)
	if err != nil {
		t.Fatalf("parse the exported document: %v", err)
	}
	if len(parsed) != 3 {
		t.Fatalf("exported entries = %d, want 3", len(parsed))
	}
	original, err := database.ParseMarkdown(seed)
	if err != nil {
		t.Fatalf("parse the fixture: %v", err)
	}
	var before, after []string
	for _, entry := range original {
		before = append(before, entry.Fingerprint)
	}
	for _, entry := range parsed {
		after = append(after, entry.Fingerprint)
	}
	sort.Strings(before)
	sort.Strings(after)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("fingerprints changed across import and export: %v != %v", after, before)
	}

	history := string(readUpgradeFixture(t, "ERRORS-history.md"))
	report, err = database.ImportMarkdown(ctx, conn, history)
	if err != nil {
		t.Fatalf("import the multi-version document: %v", err)
	}
	if report.Entries != 1 || report.IssuesCreated != 1 || report.VersionsAdded != 2 {
		t.Fatalf("multi-version import report = %+v, want 1 entry, 1 issue, 2 versions", report)
	}
	rows, err := conn.Query(ctx, `
		select sv.version, sv.body
		from solution_versions sv
		join solutions s on s.id = sv.solution_id
		join issues i on i.id = s.issue_id
		where i.fingerprint = '4ed4b05db309'
		order by sv.version`)
	if err != nil {
		t.Fatalf("read the imported versions: %v", err)
	}
	var bodies []string
	for rows.Next() {
		var version int
		var body string
		if err := rows.Scan(&version, &body); err != nil {
			t.Fatalf("scan a version row: %v", err)
		}
		if version != len(bodies)+1 {
			t.Fatalf("version %d arrived out of order", version)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the imported versions: %v", err)
	}
	want := []string{"Add a build script to package.json.", "Run pnpm run build after adding the script."}
	if !reflect.DeepEqual(bodies, want) {
		t.Fatalf("imported bodies = %q, want %q", bodies, want)
	}

	// Re-importing the same documents must not duplicate anything.
	report, err = database.ImportMarkdown(ctx, conn, seed)
	if err != nil {
		t.Fatalf("re-import the seed: %v", err)
	}
	if report.VersionsAdded != 0 {
		t.Fatalf("re-importing the seed added %d versions, want 0", report.VersionsAdded)
	}
	report, err = database.ImportMarkdown(ctx, conn, history)
	if err != nil {
		t.Fatalf("re-import the multi-version document: %v", err)
	}
	if report.VersionsAdded != 0 {
		t.Fatalf("re-importing the multi-version document added %d versions, want 0", report.VersionsAdded)
	}
}

// TestOldMarkdownIsRedactedOnImport proves imported text goes through redaction
// before it is written: an upgrade must never turn an old file into a way to
// store a raw credential.
func TestOldMarkdownIsRedactedOnImport(t *testing.T) {
	ctx := context.Background()
	conn := connect(t)
	freshSchema(t, conn)
	migrate(t, conn, migrationsDir())

	secret := "sk-" + strings.Repeat("y", 24)
	seed := string(readUpgradeFixture(t, "ERRORS-v0.md"))
	const marker = "CONTEXT_OVERFLOW: the request exceeds the model's context window"
	if !strings.Contains(seed, marker) {
		t.Fatalf("fixture no longer contains %q", marker)
	}
	tainted := strings.Replace(seed, marker, marker+" API_KEY="+secret, 1)
	if _, err := database.ImportMarkdown(ctx, conn, tainted); err != nil {
		t.Fatalf("import a tainted document: %v", err)
	}

	var stored strings.Builder
	rows, err := conn.Query(ctx, `
		select coalesce(headline, '') || ' ' || coalesce(normalized_message, '') || ' ' || coalesce(redacted_sample, '')
		from events`)
	if err != nil {
		t.Fatalf("read the stored events: %v", err)
	}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan a stored event: %v", err)
		}
		stored.WriteString(text)
		stored.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the stored events: %v", err)
	}
	rows.Close()
	var titles strings.Builder
	rows, err = conn.Query(ctx, "select coalesce(title, '') || ' ' || coalesce(trigger, '') from issues")
	if err != nil {
		t.Fatalf("read the stored issues: %v", err)
	}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan a stored issue: %v", err)
		}
		titles.WriteString(text)
		titles.WriteString("\n")
	}
	rows.Close()

	written := stored.String() + titles.String()
	if strings.Contains(written, secret) {
		t.Fatal("the raw secret reached the database; redaction did not run before the write")
	}
	if !strings.Contains(written, "<secret>") {
		t.Fatalf("the stored text has no redaction marker either:\n%s", written)
	}
}

// TestOldConnectorCapabilityAndProtocol proves a capability file from the first
// connector release still loads, its missing fields stay explicit, and a
// protocol version the core does not understand is refused with a clear error.
func TestOldConnectorCapabilityAndProtocol(t *testing.T) {
	raw := readUpgradeFixture(t, "capability-v0.json")
	capability, err := contract.LoadConnectorCapability(raw)
	if err != nil {
		t.Fatalf("load the old capability file: %v", err)
	}
	if capability.Connector != "claude-code" || capability.Channel != "A" {
		t.Fatalf("capability = %+v, want the claude-code channel A file", capability)
	}
	if !capability.Probe.OK {
		t.Fatal("the probe result did not survive the load")
	}
	degraded := capture.Degradation(capability)
	if degraded == nil {
		t.Fatal("a capability that cannot capture error_code reported no degradation")
	}
	if !strings.Contains(degraded.Error(), "error_code") {
		t.Fatalf("degradation %q does not name the missing field", degraded)
	}
	if degraded.Fallback.Channel != "B" {
		t.Fatalf("fallback channel = %q, want B", degraded.Fallback.Channel)
	}

	// A newer protocol must be refused, never downgraded to v1 silently.
	request := contract.CaptureRequest{
		SchemaVersion: "v2",
		ConnectorID:   "legacy-connector",
		SessionID:     "legacy-session",
		Observation: contract.ConnectorObservation{
			ObservedAt: "2026-10-20T00:00:00Z",
			Source:     "tool",
			Message:    "boom",
		},
	}
	if err := request.Validate(); err == nil {
		t.Fatal("a v2 capture request was accepted by a v1 core")
	} else if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error %q does not mention schema_version", err)
	}

	// An unknown channel is refused as well, with the channel named.
	capability.Channel = "F"
	if err := capability.Validate(); err == nil {
		t.Fatal("channel F was accepted")
	} else if !strings.Contains(err.Error(), "channel") {
		t.Fatalf("error %q does not mention the channel", err)
	}

}
