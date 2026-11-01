package database_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// newDatabase creates a dedicated database so a dump and a restore never share
// a schema with the rest of the tests. The name is removed on cleanup.
func newDatabase(t *testing.T, admin *pgx.Conn, prefix string) (string, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("errkb_%s_%d", prefix, time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = database.DropDatabase(context.WithoutCancel(ctx), admin, name)
	})
	cfg, err := pgx.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnv, err)
	}
	cfg.Database = name
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
	return name, conn
}

func insertConnectorNamed(t *testing.T, conn *pgx.Conn, name string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(context.Background(), `
		insert into connectors (name, channel, source, actor)
		values ($1, 'A', 'test', 'test')
		returning id`, name).Scan(&id); err != nil {
		t.Fatalf("insert connector %s: %v", name, err)
	}
	return id
}

func issueIDForProject(t *testing.T, conn *pgx.Conn, project string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(context.Background(), `
		select id from issues where project_key = $1 order by created_at limit 1`, project).Scan(&id); err != nil {
		t.Fatalf("read the issue for project %s: %v", project, err)
	}
	return id
}

func insertAuditRow(t *testing.T, conn *pgx.Conn, occurredAt time.Time, action string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(context.Background(), `
		insert into audit_log (occurred_at, action, entity, operator, source, detail)
		values ($1, $2, 'test', 'tester', 'test', '{"seed": true}'::jsonb)
		returning id`, occurredAt, action).Scan(&id); err != nil {
		t.Fatalf("insert audit row %s: %v", action, err)
	}
	return id
}

func recordEvents(t *testing.T, conn *pgx.Conn, connectorID, sessionID, project string, start int, messages ...string) {
	t.Helper()
	for i, message := range messages {
		event := recordFixture(connectorID, sessionID, eventUUID(start+i), message, project)
		if _, err := database.RecordEvent(context.Background(), conn, event); err != nil {
			t.Fatalf("record %q: %v", message, err)
		}
	}
}

// TestBackupRestoreIntoEmptyDatabase is the card's central acceptance: a dump
// restores into an empty database with the same rows, constraints and ledger.
func TestBackupRestoreIntoEmptyDatabase(t *testing.T) {
	admin := connect(t)
	ctx := context.Background()
	_, source := newDatabase(t, admin, "backup_src")
	migrate(t, source, migrationsDir())

	connectorID := insertTestConnector(t, source)
	sessionID := insertTestSession(t, source, connectorID)
	recordEvents(t, source, connectorID, sessionID, "proj", 1, "eperm rename package one", "eperm rename package one", "eperm rename package one")

	issueID := issueIDForProject(t, source, "proj")
	solutionID, err := database.CreateSolution(ctx, source, issueID, "test", "test")
	if err != nil {
		t.Fatalf("create solution: %v", err)
	}
	version, err := database.AddSolutionVersion(ctx, source, solutionID, "close the editor before renaming", "", "unsaved work is lost", database.Environment{OS: "windows"}, "test", "test")
	if err != nil {
		t.Fatalf("add solution version: %v", err)
	}
	if _, err := database.RecordOutcome(ctx, source, issueID, version.ID, database.OutcomeSuccess, "worked", database.Environment{}, "", "test", "test"); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	insertAuditRow(t, source, time.Now().UTC(), "test.seed")

	if _, err := source.Exec(ctx, `update issues set status = 'resolved' where id = $1`, issueID); err != nil {
		t.Fatalf("resolve the prevention issue: %v", err)
	}
	registered := registerPrevention(t, source, preventionRegistration(issueID))
	verifyPrevention(t, source, registered.Version.ArtifactVersionID, false)
	verifyPrevention(t, source, registered.Version.ArtifactVersionID, true)
	activatePrevention(t, source, registered.Version.ArtifactVersionID, preventionCommitA)
	changed := preventionRegistration(issueID)
	changed.ContentSHA256 = preventionHashB
	secondVersion := registerPrevention(t, source, changed)
	secondIssue := preventionIssue(t, source, "proj", "resolved")
	linked := registerPrevention(t, source, preventionRegistration(secondIssue))
	if linked.Artifact.ArtifactID != registered.Artifact.ArtifactID {
		t.Fatalf("linked artifact = %s, want %s", linked.Artifact.ArtifactID, registered.Artifact.ArtifactID)
	}

	dir := t.TempDir()
	manifest, err := database.BackupDatabase(ctx, source, dir)
	if err != nil {
		t.Fatalf("back up: %v", err)
	}
	committed, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the committed migrations: %v", err)
	}
	if manifest.LedgerRows != int64(len(committed)) {
		t.Fatalf("ledger rows = %d, want %d", manifest.LedgerRows, len(committed))
	}
	if manifest.LedgerVersion == "" {
		t.Fatal("the backup manifest carries no ledger version")
	}
	if len(manifest.SHA256) != 64 {
		t.Fatalf("sha256 = %q, want 64 hex characters", manifest.SHA256)
	}
	if manifest.Tables["events"] != 3 || manifest.Tables["issues"] != 2 {
		t.Fatalf("manifest counts = %v, want 3 events and 2 issues", manifest.Tables)
	}
	for table, want := range map[string]int64{
		"prevention_artifacts": 1, "prevention_artifact_versions": 2,
		"issue_preventions": 2, "prevention_verifications": 2,
	} {
		if manifest.Tables[table] != want {
			t.Errorf("manifest %s = %d, want %d", table, manifest.Tables[table], want)
		}
	}
	dumpPath := filepath.Join(dir, manifest.Dump)
	info, err := os.Stat(dumpPath)
	if err != nil || info.Size() != manifest.SizeBytes {
		t.Fatalf("dump size = %v (%v), manifest says %d", info, err, manifest.SizeBytes)
	}
	manifestPath := dumpPath + ".json"
	if _, err := database.ReadManifest(manifestPath); err != nil {
		t.Fatalf("read the manifest back: %v", err)
	}

	// RestoreDatabase creates the target itself: "empty database" means the
	// database must not exist yet, not that it must exist with no rows.
	restored := fmt.Sprintf("errkb_backup_dst_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = database.DropDatabase(context.WithoutCancel(ctx), admin, restored)
	})
	report, err := database.RestoreDatabase(ctx, admin, manifestPath, restored)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	restoredCfg, err := pgx.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseEnv, err)
	}
	restoredCfg.Database = restored
	restoredConn, err := pgx.ConnectConfig(ctx, restoredCfg)
	if err != nil {
		t.Fatalf("connect to the restored database: %v", err)
	}
	t.Cleanup(func() { _ = restoredConn.Close(context.WithoutCancel(ctx)) })
	if report.LedgerRows != manifest.LedgerRows || report.LedgerVersion != manifest.LedgerVersion {
		t.Fatalf("restored ledger = %d/%s, want %d/%s", report.LedgerRows, report.LedgerVersion, manifest.LedgerRows, manifest.LedgerVersion)
	}
	for _, table := range database.BusinessTables {
		if got := rowCount(t, restoredConn, table); int64(got) != manifest.Tables[table] {
			t.Errorf("%s rows after restore = %d, want %d", table, got, manifest.Tables[table])
		}
	}
	var body string
	if err := restoredConn.QueryRow(ctx, `
		select sv.body from solution_versions sv
		join solutions s on s.id = sv.solution_id
		order by sv.version limit 1`).Scan(&body); err != nil {
		t.Fatalf("read the restored solution: %v", err)
	}
	if body != "close the editor before renaming" {
		t.Fatalf("restored solution body = %q", body)
	}
	var title string
	if err := restoredConn.QueryRow(ctx, "select title from issues limit 1").Scan(&title); err != nil || title == "" {
		t.Fatalf("restored issue title = %q (%v)", title, err)
	}
	detail, err := database.GetPrevention(ctx, restoredConn, registered.Artifact.ArtifactID)
	if err != nil {
		t.Fatalf("read the restored prevention: %v", err)
	}
	if detail.Artifact.ProjectKey != "proj" || detail.Artifact.Kind != "regression_test" ||
		detail.Artifact.Title != "rename regression" || len(detail.Artifact.IssueIDs) != 2 ||
		len(detail.Versions) != 2 || len(detail.Verifications) != 2 {
		t.Fatalf("restored prevention detail = %+v", detail)
	}
	versions := map[int]database.PreventionVersion{}
	for _, version := range detail.Versions {
		versions[version.Version] = version
	}
	active := versions[1]
	if active.ArtifactVersionID != registered.Version.ArtifactVersionID || active.Status != "active" ||
		active.ContentSHA256 != preventionHashA || active.CommitSHA == nil || *active.CommitSHA != preventionCommitA ||
		active.ActivatedAt == nil || active.ArtifactPath != "tests/rename_test.go" {
		t.Fatalf("restored active version = %+v", active)
	}
	draft := versions[2]
	if draft.ArtifactVersionID != secondVersion.Version.ArtifactVersionID || draft.Status != "draft" ||
		draft.ContentSHA256 != preventionHashB || draft.CommitSHA != nil || draft.ActivatedAt != nil {
		t.Fatalf("restored draft version = %+v", draft)
	}
	verificationOutcomes := map[bool]int{}
	for _, verification := range detail.Verifications {
		verificationOutcomes[verification.Passed]++
		if verification.ArtifactVersionID != active.ArtifactVersionID || verification.ContentSHA256 != preventionHashA ||
			verification.Evidence != "verification evidence" || verification.Environment["os"] != "windows" ||
			verification.DurationMS != 12 || verification.ExitCode == nil {
			t.Fatalf("restored verification = %+v", verification)
		}
	}
	if verificationOutcomes[true] != 1 || verificationOutcomes[false] != 1 {
		t.Fatalf("restored verification outcomes = %v", verificationOutcomes)
	}
	if got, want := preventionAuditCount(t, restoredConn), preventionAuditCount(t, source); got != want || got < 6 {
		t.Fatalf("restored prevention audit rows = %d, source = %d", got, want)
	}

	// Restoring twice into the same database must be refused: an empty database
	// is required, so a half-written restore cannot be layered over live data.
	if _, err := database.RestoreDatabase(ctx, admin, manifestPath, restored); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("restore into an existing database: %v, want an already-exists refusal", err)
	}

	// A tampered dump must fail the checksum instead of restoring silently.
	tamperedPath := filepath.Join(dir, "tampered.dump")
	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read the dump: %v", err)
	}
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(tamperedPath, raw, 0o644); err != nil {
		t.Fatalf("write the tampered dump: %v", err)
	}
	tamperedManifest := manifest
	tamperedManifest.Dump = "tampered.dump"
	if _, err := database.RestoreDatabase(ctx, admin, writeTamperedManifest(t, dir, tamperedManifest), "errkb_never_created_"+fmt.Sprint(time.Now().UnixNano())); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("restore a tampered dump: %v, want a checksum failure", err)
	}
}

// writeTamperedManifest stores a manifest pointing at a modified dump so the
// checksum comparison runs against the original digest.
func writeTamperedManifest(t *testing.T, dir string, manifest database.BackupManifest) string {
	t.Helper()
	path := filepath.Join(dir, "tampered.dump.json")
	encoded := fmt.Sprintf(`{
  "version": %d,
  "database": %q,
  "schema": %q,
  "created_at": %q,
  "dump": %q,
  "sha256": %q,
  "size_bytes": %d,
  "tables": {},
  "ledger_rows": %d,
  "ledger_version": %q
}`, manifest.Version, manifest.Database, manifest.Schema, manifest.CreatedAt.Format(time.RFC3339Nano), manifest.Dump, manifest.SHA256, manifest.SizeBytes, manifest.LedgerRows, manifest.LedgerVersion)
	if err := os.WriteFile(path, []byte(encoded), 0o644); err != nil {
		t.Fatalf("write the tampered manifest: %v", err)
	}
	return path
}

func TestDeleteByProjectKeepsAudit(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()
	recordEvents(t, conn, connectorID, sessionID, "alpha", 1, "eperm rename alpha one", "eperm rename alpha one")
	recordEvents(t, conn, connectorID, sessionID, "beta", 10, "eperm rename beta one")
	survivor := insertAuditRow(t, conn, time.Now().UTC(), "test.survivor")
	alphaIssue := issueIDForProject(t, conn, "alpha")
	if _, err := conn.Exec(ctx, `update issues set status = 'resolved' where id = $1`, alphaIssue); err != nil {
		t.Fatalf("resolve deletion fixture issue: %v", err)
	}
	secondAlphaIssue := preventionIssue(t, conn, "alpha", "resolved")
	registered := registerPrevention(t, conn, preventionRegistration(alphaIssue))
	verifyPrevention(t, conn, registered.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, registered.Version.ArtifactVersionID, preventionCommitA)
	registerPrevention(t, conn, preventionRegistration(secondAlphaIssue))

	if _, err := database.PreviewDelete(ctx, conn, database.DeleteScope{}); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("preview without a filter = %v, want a rejection", err)
	}

	scope := database.DeleteScope{ProjectKey: "alpha", Actor: "owner"}
	preview, err := database.PreviewDelete(ctx, conn, scope)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Issues != 2 || preview.Events != 2 || preview.Occurrences != 2 ||
		preview.PreventionArtifacts != 1 || preview.PreventionArtifactVersions != 1 ||
		preview.IssuePreventions != 2 || preview.PreventionVerifications != 1 {
		t.Fatalf("preview = %+v, want issue evidence and the complete prevention chain", preview)
	}
	counts, err := database.DeleteScoped(ctx, conn, scope)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if counts != preview {
		t.Fatalf("deleted %+v, preview said %+v", counts, preview)
	}
	if got := queryInt(t, conn, `select count(*) from events where project_key = 'alpha'`); got != 0 {
		t.Errorf("alpha events after delete = %d, want 0", got)
	}
	if got := queryInt(t, conn, `select count(*) from events where project_key = 'beta'`); got != 1 {
		t.Errorf("beta events after delete = %d, want 1", got)
	}
	if got := queryInt(t, conn, `select count(*) from issues where project_key = 'beta'`); got != 1 {
		t.Errorf("beta issues after delete = %d, want 1", got)
	}
	if got := rowCount(t, conn, "prevention_artifacts"); got != 0 {
		t.Errorf("prevention artifacts after project delete = %d, want 0", got)
	}
	if got := queryInt(t, conn, fmt.Sprintf(`select count(*) from issue_preventions where issue_id = '%s'`, secondAlphaIssue)); got != 0 {
		t.Errorf("cross-issue links after project delete = %d, want 0", got)
	}
	// The audit trail survives business deletion, including the row the delete
	// itself just wrote.
	if got := queryInt(t, conn, fmt.Sprintf(`select count(*) from audit_log where id = %d`, survivor)); got != 1 {
		t.Errorf("the older audit row is gone")
	}
	var operator, action string
	var detail string
	if err := conn.QueryRow(ctx, `
		select operator, action, detail::text from audit_log
		where action = 'delete.scope' order by id desc limit 1`).Scan(&operator, &action, &detail); err != nil {
		t.Fatalf("read the delete audit row: %v", err)
	}
	if operator != "owner" {
		t.Errorf("delete actor = %q, want owner", operator)
	}
	for _, want := range []string{
		`"events": 2`, `"issues": 2`, `"project_key": "alpha"`,
		`"prevention_artifacts": 1`, `"prevention_artifact_versions": 1`,
		`"issue_preventions": 2`, `"prevention_verifications": 1`,
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("delete audit detail %s does not contain %s", detail, want)
		}
	}
}

func TestDeleteByTimeBoundaryIsExclusive(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	for _, observed := range []time.Time{cutoff.Add(-time.Hour), cutoff, cutoff.Add(time.Hour)} {
		event := recordFixture(connectorID, sessionID, eventUUID(int(observed.Unix())), fmt.Sprintf("eperm rename at %s", observed.Format(time.RFC3339)), "proj")
		event.ObservedAt = observed.Format(time.RFC3339)
		if _, err := database.RecordEvent(ctx, conn, event); err != nil {
			t.Fatalf("record at %s: %v", observed, err)
		}
	}
	var issueID string
	if err := conn.QueryRow(ctx, `select o.issue_id from occurrences o join events e on e.event_id = o.event_id where e.observed_at = $1`, cutoff.Add(-time.Hour)).Scan(&issueID); err != nil {
		t.Fatalf("read old issue: %v", err)
	}
	if _, err := conn.Exec(ctx, `update issues set status = 'resolved' where id = $1`, issueID); err != nil {
		t.Fatal(err)
	}
	registered := registerPrevention(t, conn, preventionRegistration(issueID))
	verifyPrevention(t, conn, registered.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, registered.Version.ArtifactVersionID, preventionCommitA)
	scope := database.DeleteScope{Before: &cutoff, Actor: "owner"}
	preview, err := database.PreviewDelete(ctx, conn, scope)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Events != 1 || preview.Occurrences != 1 || preview.PreventionArtifacts != 0 ||
		preview.PreventionArtifactVersions != 0 || preview.IssuePreventions != 0 || preview.PreventionVerifications != 0 {
		t.Fatalf("preview = %+v, want one old event and no prevention rows", preview)
	}
	counts, err := database.DeleteScoped(ctx, conn, scope)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if counts != preview {
		t.Fatalf("deleted %+v, preview said %+v", counts, preview)
	}
	var remaining int
	if err := conn.QueryRow(ctx, `select count(*) from events where observed_at = $1`, cutoff).Scan(&remaining); err != nil {
		t.Fatalf("count the boundary event: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("the event at the cutoff boundary was deleted; the comparison must be exclusive")
	}
	if got := rowCount(t, conn, "events"); got != 2 {
		t.Fatalf("events after delete = %d, want 2", got)
	}
	version, err := database.GetPreventionVersion(ctx, conn, registered.Version.ArtifactVersionID)
	if err != nil || version.Status != "active" {
		t.Fatalf("time deletion changed active prevention: %+v, %v", version, err)
	}
}

func TestDeleteByConnector(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()
	secondConnector := insertConnectorNamed(t, conn, "record-test-second")
	secondSession := insertTestSession(t, conn, secondConnector)
	recordEvents(t, conn, connectorID, sessionID, "proj", 1, "first connector failure")
	recordEvents(t, conn, secondConnector, secondSession, "proj", 10, "second connector failure")
	var issueID string
	if err := conn.QueryRow(ctx, `select issue_id from occurrences where connector_id = $1`, connectorID).Scan(&issueID); err != nil {
		t.Fatalf("read connector issue: %v", err)
	}
	if _, err := conn.Exec(ctx, `update issues set status = 'resolved' where id = $1`, issueID); err != nil {
		t.Fatal(err)
	}
	registered := registerPrevention(t, conn, preventionRegistration(issueID))
	verifyPrevention(t, conn, registered.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, registered.Version.ArtifactVersionID, preventionCommitA)

	scope := database.DeleteScope{ConnectorID: connectorID, Actor: "owner"}
	preview, err := database.PreviewDelete(ctx, conn, scope)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Events != 1 || preview.Occurrences != 1 || preview.PreventionArtifacts != 0 ||
		preview.PreventionArtifactVersions != 0 || preview.IssuePreventions != 0 || preview.PreventionVerifications != 0 {
		t.Fatalf("preview = %+v, want evidence only and no prevention rows", preview)
	}
	counts, err := database.DeleteScoped(ctx, conn, scope)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if counts != preview {
		t.Fatalf("deleted %+v, preview said %+v", counts, preview)
	}
	if got := queryInt(t, conn, fmt.Sprintf(`select count(*) from events where connector_id = '%s'`, connectorID)); got != 0 {
		t.Errorf("the deleted connector still has %d events", got)
	}
	if got := rowCount(t, conn, "events"); got != 1 {
		t.Errorf("events after delete = %d, want 1", got)
	}
	// Issues aggregate across connectors: deleting one connector's evidence does
	// not silently remove the issue history of the other connector.
	if got := rowCount(t, conn, "issues"); got != 2 {
		t.Errorf("issues after delete = %d, want 2", got)
	}
	version, err := database.GetPreventionVersion(ctx, conn, registered.Version.ArtifactVersionID)
	if err != nil || version.Status != "active" {
		t.Fatalf("connector deletion changed active prevention: %+v, %v", version, err)
	}
}

func TestRetentionSplitsThreeClasses(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, spec := range []struct {
		message  string
		observed time.Time
	}{
		{"eperm rename old " + fmt.Sprint(1111), now.Add(-100 * 24 * time.Hour)},
		{"eperm rename mid " + fmt.Sprint(2222), now.Add(-10 * 24 * time.Hour)},
		{"eperm rename fresh " + fmt.Sprint(3333), now},
	} {
		event := recordFixture(connectorID, sessionID, eventUUID(int(spec.observed.Unix())), spec.message, "proj")
		event.ObservedAt = spec.observed.Format(time.RFC3339)
		if _, err := database.RecordEvent(ctx, conn, event); err != nil {
			t.Fatalf("record %s: %v", spec.observed, err)
		}
	}
	insertAuditRow(t, conn, now.Add(-400*24*time.Hour), "test.old-audit")
	freshAudit := insertAuditRow(t, conn, now, "test.fresh-audit")

	report, err := database.ApplyRetention(ctx, conn, database.RetentionPolicy{SampleDays: 7, NormalizedDays: 90, AuditDays: 365}, now)
	if err != nil {
		t.Fatalf("apply retention: %v", err)
	}
	if report.SamplesCleared != 1 || report.EventsRemoved != 1 || report.AuditRowsRemoved != 1 {
		t.Fatalf("retention report = %+v, want 1 sample, 1 event, 1 audit row", report)
	}
	if got := rowCount(t, conn, "events"); got != 2 {
		t.Fatalf("events after retention = %d, want 2", got)
	}
	var midSample, freshSample string
	if err := conn.QueryRow(ctx, `select redacted_sample from events where observed_at < $1 order by observed_at desc limit 1`,
		now.Add(-24*time.Hour).Format(time.RFC3339)).Scan(&midSample); err != nil {
		t.Fatalf("read the mid sample: %v", err)
	}
	if midSample != "" {
		t.Errorf("the old sample is still stored: %q", midSample)
	}
	if err := conn.QueryRow(ctx, `select redacted_sample from events order by observed_at desc limit 1`).Scan(&freshSample); err != nil {
		t.Fatalf("read the fresh sample: %v", err)
	}
	if freshSample == "" {
		t.Error("the fresh sample was cleared; only samples older than the sample cutoff may be cleared")
	}
	if got := queryInt(t, conn, fmt.Sprintf(`select count(*) from audit_log where id = %d`, freshAudit)); got != 1 {
		t.Error("the recent audit row was removed by retention")
	}
	if got := queryInt(t, conn, `select count(*) from audit_log where action = 'retention.purge'`); got != 1 {
		t.Errorf("retention purge audit rows = %d, want 1", got)
	}

	// The audit guard is what makes "audit survives business deletion" true: a
	// plain delete outside the retention job is rejected.
	_, err = conn.Exec(ctx, `delete from audit_log`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
		t.Fatalf("direct audit delete = %v, want SQLSTATE 55000 from the guard", err)
	}
}

func TestRetentionKeepsActivePreventionAndUsesRemainingOccurrences(t *testing.T) {
	conn, _ := preventionSchema(t)
	ctx := context.Background()
	now := time.Now().UTC()
	issueID := preventionIssue(t, conn, "proj", "resolved")
	registered := preventionActiveAt(t, conn, preventionRegistration(issueID), now.Add(-200*24*time.Hour))
	preventionOccurrence(t, conn, issueID, now.Add(-100*24*time.Hour))
	preventionOccurrence(t, conn, issueID, now)

	before, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{IssueID: issueID}, now)
	if err != nil || len(before.Activations) != 1 || before.Activations[0].RecurrenceCount != 2 {
		t.Fatalf("stats before retention = %+v, %v", before, err)
	}
	report, err := database.ApplyRetention(ctx, conn, database.RetentionPolicy{SampleDays: 7, NormalizedDays: 90, AuditDays: 365}, now)
	if err != nil {
		t.Fatalf("apply retention: %v", err)
	}
	if report.EventsRemoved != 1 {
		t.Fatalf("retention report = %+v, want the one expired occurrence event", report)
	}
	version, err := database.GetPreventionVersion(ctx, conn, registered.Version.ArtifactVersionID)
	if err != nil || version.Status != "active" {
		t.Fatalf("retention changed active prevention: %+v, %v", version, err)
	}
	after, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{IssueID: issueID}, now)
	if err != nil || len(after.Activations) != 1 {
		t.Fatalf("stats after retention = %+v, %v", after, err)
	}
	activation := after.Activations[0]
	// The surviving occurrence is outside the activation's 30-day after window,
	// but it remains visible in the all-time recurrence count.
	if activation.RecurrenceCount != 1 || activation.AfterCount != 0 || activation.ObservationStatus != "mature" ||
		after.MatureActivations != 1 || after.MatureZeroRecurrence != 1 || after.MatureProtectionRate == nil || *after.MatureProtectionRate != 1 {
		t.Fatalf("post-retention stats do not reflect remaining occurrences: %+v", after)
	}
}

func queryInt(t *testing.T, conn *pgx.Conn, query string) int {
	t.Helper()
	var value int
	if err := conn.QueryRow(context.Background(), query).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}
