package database_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

const (
	preventionHashA   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	preventionHashB   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	preventionCommitA = "1111111111111111111111111111111111111111"
	preventionCommitB = "2222222222222222222222222222222222222222"
)

func preventionSchema(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	conn := connect(t)
	schema := freshSchema(t, conn)
	migrate(t, conn, migrationsDir())
	return conn, schema
}

var preventionIssueSequence atomic.Uint64

func preventionIssue(t *testing.T, conn *pgx.Conn, project, status string) string {
	t.Helper()
	var id string
	fingerprint := fmt.Sprintf("%012x", preventionIssueSequence.Add(1))
	if err := conn.QueryRow(context.Background(), `
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		values ($1, $2, 'prevention fixture', 'tool', $3, '', 1, 'test', 'test') returning id
	`, fingerprint, project, status).Scan(&id); err != nil {
		t.Fatalf("insert prevention issue: %v", err)
	}
	return id
}

func preventionRegistration(issueID string) database.PreventionRegistration {
	return database.PreventionRegistration{
		IssueID: issueID, Kind: "regression_test", Title: "rename regression",
		ArtifactPath: "tests/rename_test.go", ContentSHA256: preventionHashA,
		Source: "test", Actor: "owner",
	}
}

func registerPrevention(t *testing.T, conn *pgx.Conn, registration database.PreventionRegistration) database.PreventionRegistrationResult {
	t.Helper()
	result, err := database.RegisterPrevention(context.Background(), conn, registration)
	if err != nil {
		t.Fatalf("register prevention: %v", err)
	}
	return result
}

func verifyPrevention(t *testing.T, conn *pgx.Conn, versionID string, passed bool) database.PreventionVerificationResult {
	t.Helper()
	exitCode := 0
	if !passed {
		exitCode = 1
	}
	result, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: versionID, Command: []string{"go", "test", "./..."},
		ExitCode: &exitCode, Passed: passed, DurationMS: 12,
		Evidence: "verification evidence", Environment: map[string]string{"os": "windows"},
		ContentSHA256: preventionHashA, Source: "test", Actor: "owner",
	})
	if err != nil {
		t.Fatalf("record prevention verification: %v", err)
	}
	return result
}

func activatePrevention(t *testing.T, conn *pgx.Conn, versionID, commit string) (database.PreventionVersion, bool) {
	t.Helper()
	version, activated, err := database.ActivatePrevention(context.Background(), conn, versionID, commit)
	if err != nil {
		t.Fatalf("activate prevention: %v", err)
	}
	return version, activated
}

func preventionAuditCount(t *testing.T, conn *pgx.Conn) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), `select count(*) from audit_log where action like 'prevention.%'`).Scan(&count); err != nil {
		t.Fatalf("count prevention audit: %v", err)
	}
	return count
}

func TestPreventionRegistrationRequiresResolvedIssue(t *testing.T) {
	conn, _ := preventionSchema(t)
	for _, status := range []string{"open", "wontfix", "misjudged"} {
		t.Run(status, func(t *testing.T) {
			issueID := preventionIssue(t, conn, "proj-a", status)
			_, err := database.RegisterPrevention(context.Background(), conn, preventionRegistration(issueID))
			if !errors.Is(err, database.ErrPreventionConflict) {
				t.Fatalf("register against %s issue = %v, want conflict", status, err)
			}
		})
	}
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	result := registerPrevention(t, conn, preventionRegistration(issueID))
	if !result.Created || result.Artifact.ProjectKey != "proj-a" || result.Version.Status != "draft" {
		t.Fatalf("resolved registration = %+v", result)
	}
	if len(result.Artifact.IssueIDs) != 1 || result.Artifact.IssueIDs[0] != issueID {
		t.Fatalf("artifact issue links = %v", result.Artifact.IssueIDs)
	}
	if got := preventionAuditCount(t, conn); got != 3 {
		t.Fatalf("new registration audit rows = %d, want 3", got)
	}
}

func TestPreventionRegistrationIsIdempotentAndAppendsChangedHash(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	first := registerPrevention(t, conn, preventionRegistration(issueID))
	audits := preventionAuditCount(t, conn)
	repeated := registerPrevention(t, conn, preventionRegistration(issueID))
	if repeated.Created || repeated.Version.ArtifactVersionID != first.Version.ArtifactVersionID {
		t.Fatalf("idempotent registration = %+v, first = %+v", repeated, first)
	}
	if got := preventionAuditCount(t, conn); got != audits {
		t.Fatalf("idempotent registration added audit: %d then %d", audits, got)
	}
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, first.Version.ArtifactVersionID, preventionCommitA)
	changed := preventionRegistration(issueID)
	changed.ContentSHA256 = preventionHashB
	second := registerPrevention(t, conn, changed)
	if !second.Created || second.Version.Version != 2 || second.Version.Status != "draft" {
		t.Fatalf("changed registration = %+v", second)
	}
	old, err := database.GetPreventionVersion(context.Background(), conn, first.Version.ArtifactVersionID)
	if err != nil || old.Status != "active" {
		t.Fatalf("changed hash disturbed active version: %+v, %v", old, err)
	}
}

func TestPreventionConcurrentRegistrationCreatesOneArtifact(t *testing.T) {
	conn, schema := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	config, err := pgx.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatalf("parse database config: %v", err)
	}
	config.RuntimeParams["search_path"] = schema
	const workers = 2
	var wait sync.WaitGroup
	errorsCh := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			other, err := pgx.ConnectConfig(context.Background(), config)
			if err != nil {
				errorsCh <- err
				return
			}
			defer other.Close(context.Background())
			_, err = database.RegisterPrevention(context.Background(), other, preventionRegistration(issueID))
			errorsCh <- err
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent registration: %v", err)
		}
	}
	for table, want := range map[string]int{"prevention_artifacts": 1, "issue_preventions": 1, "prevention_artifact_versions": 1} {
		if got := rowCount(t, conn, table); got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
	if got := preventionAuditCount(t, conn); got != 3 {
		t.Fatalf("concurrent registration audit rows = %d, want 3", got)
	}
}

func TestPreventionArtifactCanLinkIssuesButNotAcrossProjects(t *testing.T) {
	conn, _ := preventionSchema(t)
	firstIssue := preventionIssue(t, conn, "proj-a", "resolved")
	first := registerPrevention(t, conn, preventionRegistration(firstIssue))
	secondIssue := preventionIssue(t, conn, "proj-a", "resolved")
	second := registerPrevention(t, conn, preventionRegistration(secondIssue))
	if second.Artifact.ArtifactID != first.Artifact.ArtifactID || !second.Created || len(second.Artifact.IssueIDs) != 2 {
		t.Fatalf("second issue link = %+v", second)
	}
	otherProject := preventionIssue(t, conn, "proj-b", "resolved")
	_, err := database.RegisterPrevention(context.Background(), conn, preventionRegistration(otherProject))
	if !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("cross-project link = %v, want conflict", err)
	}
	missing := preventionRegistration("11111111-1111-4111-8111-111111111111")
	if _, err := database.RegisterPrevention(context.Background(), conn, missing); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing issue = %v, want not found", err)
	}
}

func TestPreventionVerificationStateMachineAndAudit(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	registered := registerPrevention(t, conn, preventionRegistration(issueID))
	versionID := registered.Version.ArtifactVersionID
	if _, _, err := database.ActivatePrevention(context.Background(), conn, versionID, preventionCommitA); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("activate draft = %v, want conflict", err)
	}
	passed := verifyPrevention(t, conn, versionID, true)
	if passed.Version.Status != "verified" || !passed.Verification.Passed || passed.Verification.ExitCode == nil || *passed.Verification.ExitCode != 0 {
		t.Fatalf("passed verification = %+v", passed)
	}
	failed := verifyPrevention(t, conn, versionID, false)
	if failed.Version.Status != "verified" || failed.Verification.Passed || failed.Version.LatestVerification == nil || failed.Version.LatestVerification.Passed {
		t.Fatalf("failed re-verification = %+v", failed)
	}
	if _, _, err := database.ActivatePrevention(context.Background(), conn, versionID, preventionCommitA); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("activate after latest failure = %v, want conflict", err)
	}
	verifyPrevention(t, conn, versionID, true)
	active, activated := activatePrevention(t, conn, versionID, preventionCommitA)
	if !activated || active.Status != "active" || active.CommitSHA == nil || *active.CommitSHA != preventionCommitA {
		t.Fatalf("active version = %+v, activated=%v", active, activated)
	}
	again, activated := activatePrevention(t, conn, versionID, preventionCommitA)
	if activated || again.Status != "active" {
		t.Fatalf("idempotent activation = %+v, activated=%v", again, activated)
	}
	if _, _, err := database.ActivatePrevention(context.Background(), conn, versionID, preventionCommitB); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("reactivate at another commit = %v, want conflict", err)
	}
	if _, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: versionID, Command: []string{"go", "test"}, ContentSHA256: preventionHashA,
	}); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("verify active = %v, want conflict", err)
	}
	if got := rowCount(t, conn, "prevention_verifications"); got != 3 {
		t.Fatalf("verification rows = %d, want 3", got)
	}
	if got := preventionAuditCount(t, conn); got != 7 {
		t.Fatalf("audit rows after three verifies and one activation = %d, want 7", got)
	}
}

func TestPreventionActivationDeprecatesOldActiveAndForbidsRevival(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	first := registerPrevention(t, conn, preventionRegistration(issueID))
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, first.Version.ArtifactVersionID, preventionCommitA)
	changed := preventionRegistration(issueID)
	changed.ContentSHA256 = preventionHashB
	second := registerPrevention(t, conn, changed)
	input := database.PreventionVerificationInput{
		ArtifactVersionID: second.Version.ArtifactVersionID, Command: []string{"go", "test"},
		ContentSHA256: preventionHashB, Passed: true, Source: "test", Actor: "owner",
	}
	if _, err := database.RecordPreventionVerification(context.Background(), conn, input); err != nil {
		t.Fatalf("verify second version: %v", err)
	}
	activatePrevention(t, conn, second.Version.ArtifactVersionID, preventionCommitB)
	old, err := database.GetPreventionVersion(context.Background(), conn, first.Version.ArtifactVersionID)
	if err != nil || old.Status != "deprecated" || old.CommitSHA == nil || *old.CommitSHA != preventionCommitA || old.ActivatedAt == nil {
		t.Fatalf("old active after replacement = %+v, %v", old, err)
	}
	if _, _, err := database.ActivatePrevention(context.Background(), conn, first.Version.ArtifactVersionID, preventionCommitA); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("revive deprecated = %v, want conflict", err)
	}
	if _, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: first.Version.ArtifactVersionID, Command: []string{"go", "test"}, ContentSHA256: preventionHashA,
	}); !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("verify deprecated = %v, want conflict", err)
	}
}

func TestPreventionConcurrentActivationLeavesAtMostOneActive(t *testing.T) {
	conn, schema := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	first := registerPrevention(t, conn, preventionRegistration(issueID))
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, true)
	changed := preventionRegistration(issueID)
	changed.ContentSHA256 = preventionHashB
	second := registerPrevention(t, conn, changed)
	if _, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: second.Version.ArtifactVersionID, Command: []string{"go", "test"},
		ContentSHA256: preventionHashB, Passed: true,
	}); err != nil {
		t.Fatalf("verify second version: %v", err)
	}
	config, err := pgx.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	versions := []string{first.Version.ArtifactVersionID, second.Version.ArtifactVersionID}
	var wait sync.WaitGroup
	errorsCh := make(chan error, len(versions))
	for index, versionID := range versions {
		wait.Add(1)
		go func(index int, versionID string) {
			defer wait.Done()
			other, err := pgx.ConnectConfig(context.Background(), config)
			if err != nil {
				errorsCh <- err
				return
			}
			defer other.Close(context.Background())
			commit := preventionCommitA
			if index == 1 {
				commit = preventionCommitB
			}
			_, _, err = database.ActivatePrevention(context.Background(), other, versionID, commit)
			if err != nil && !errors.Is(err, database.ErrPreventionConflict) {
				errorsCh <- err
				return
			}
			errorsCh <- nil
		}(index, versionID)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent activation: %v", err)
		}
	}
	var active int
	if err := conn.QueryRow(context.Background(), `select count(*) from prevention_artifact_versions where status = 'active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active versions after concurrent activation = %d, want 1", active)
	}
}

func TestPreventionRejectsVerificationHashMismatch(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	registered := registerPrevention(t, conn, preventionRegistration(issueID))
	_, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: registered.Version.ArtifactVersionID, Command: []string{"go", "test"},
		ContentSHA256: preventionHashB, Passed: true,
	})
	if !errors.Is(err, database.ErrPreventionConflict) {
		t.Fatalf("verification hash mismatch = %v, want conflict", err)
	}
	if got := rowCount(t, conn, "prevention_verifications"); got != 0 {
		t.Fatalf("hash mismatch wrote %d verification rows", got)
	}
}

func TestPreventionRedactsRegistrationAndVerification(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	registration := preventionRegistration(issueID)
	registration.Title = "token sk-" + "abcdefghijklmnopqrstuvwx"
	registration.Source = "source sk-" + "abcdefghijklmnopqrstuvwx"
	registration.Actor = "actor sk-" + "abcdefghijklmnopqrstuvwx"
	registered := registerPrevention(t, conn, registration)
	if strings.Contains(registered.Artifact.Title, "sk-") {
		t.Fatalf("title was not redacted: %q", registered.Artifact.Title)
	}
	var source, actor string
	if err := conn.QueryRow(context.Background(), `select source, actor from prevention_artifacts where id = $1`, registered.Artifact.ArtifactID).Scan(&source, &actor); err != nil {
		t.Fatalf("read registration redaction: %v", err)
	}
	if strings.Contains(source+actor, "sk-") {
		t.Fatalf("source/actor were not redacted: %q %q", source, actor)
	}
	verification, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: registered.Version.ArtifactVersionID,
		Command:           []string{"tool", "sk-" + "abcdefghijklmnopqrstuvwx"}, Evidence: "auth sk-" + "abcdefghijklmnopqrstuvwx",
		ContentSHA256: preventionHashA, Environment: map[string]string{"os": "windows"},
	})
	if err != nil {
		t.Fatalf("record sensitive verification: %v", err)
	}
	if strings.Contains(strings.Join(verification.Verification.Command, " ")+verification.Verification.Evidence, "sk-") {
		t.Fatalf("verification was not redacted: %+v", verification)
	}
}

func TestPreventionListAndGetHaveStablePagesAndArrays(t *testing.T) {
	conn, _ := preventionSchema(t)
	issueID := preventionIssue(t, conn, "proj-a", "resolved")
	first := registerPrevention(t, conn, preventionRegistration(issueID))
	other := preventionRegistration(issueID)
	other.Kind = "agent_instruction"
	other.ArtifactPath = "AGENTS.md"
	second := registerPrevention(t, conn, other)
	if _, err := conn.Exec(context.Background(), `update prevention_artifacts set updated_at = '2026-01-01T00:00:00Z'`); err != nil {
		t.Fatalf("align updated_at for cursor test: %v", err)
	}
	page, cursor, err := database.ListPreventions(context.Background(), conn, database.PreventionFilter{Limit: 1})
	if err != nil || len(page) != 1 || cursor == "" || page[0].Versions == nil {
		t.Fatalf("first prevention page = %+v, cursor=%q, err=%v", page, cursor, err)
	}
	remaining, next, err := database.ListPreventions(context.Background(), conn, database.PreventionFilter{Limit: 10, Cursor: cursor})
	if err != nil || len(remaining) != 1 || next != "" || remaining[0].Versions == nil {
		t.Fatalf("second prevention page = %+v, next=%q, err=%v", remaining, next, err)
	}
	ids := []string{first.Artifact.ArtifactID, second.Artifact.ArtifactID}
	if page[0].Artifact.ArtifactID == remaining[0].Artifact.ArtifactID {
		t.Fatalf("cursor returned duplicate artifact %v", ids)
	}
	filtered, _, err := database.ListPreventions(context.Background(), conn, database.PreventionFilter{IssueID: issueID, ProjectKey: "proj-a", Kind: "agent_instruction", Status: "draft"})
	if err != nil || len(filtered) != 1 || filtered[0].Artifact.Kind != "agent_instruction" {
		t.Fatalf("filtered preventions = %+v, %v", filtered, err)
	}
	empty, _, err := database.ListPreventions(context.Background(), conn, database.PreventionFilter{ProjectKey: "missing"})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty prevention list = %#v, %v", empty, err)
	}
	detail, err := database.GetPrevention(context.Background(), conn, first.Artifact.ArtifactID)
	if err != nil || detail.Versions == nil || detail.Verifications == nil || len(detail.Versions) != 1 {
		t.Fatalf("prevention detail = %+v, %v", detail, err)
	}
	if _, err := database.GetPrevention(context.Background(), conn, "11111111-1111-4111-8111-111111111111"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing prevention detail = %v, want not found", err)
	}
}

func preventionOccurrence(t *testing.T, conn *pgx.Conn, issueID string, occurredAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var projectKey, connectorID, eventID string
	if err := conn.QueryRow(ctx, `select project_key from issues where id = $1`, issueID).Scan(&projectKey); err != nil {
		t.Fatalf("read occurrence issue: %v", err)
	}
	name := fmt.Sprintf("prevention-stats-%d", preventionIssueSequence.Add(1))
	if err := conn.QueryRow(ctx, `insert into connectors (name, channel, source, actor) values ($1, 'A', 'test', 'test') returning id`, name).Scan(&connectorID); err != nil {
		t.Fatalf("insert occurrence connector: %v", err)
	}
	fingerprint := fmt.Sprintf("%012x", preventionIssueSequence.Add(1))
	if err := conn.QueryRow(ctx, `
		insert into events (connector_id, observed_at, channel, category, severity, headline,
			normalized_message, redacted_sample, os, project_key, fingerprint, evidence, confidence, source, actor)
		values ($1, $2, 'A', 'tool', 'error', 'stats occurrence', 'stats occurrence', 'stats occurrence',
			'windows', $3, $4, 'stats', 1, 'test', 'test') returning event_id
	`, connectorID, occurredAt, projectKey, fingerprint).Scan(&eventID); err != nil {
		t.Fatalf("insert occurrence event: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		insert into occurrences (issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
		values ($1, $2, $3, $4, '{}'::jsonb, $5, 'test', 'test')
	`, issueID, eventID, connectorID, projectKey, occurredAt); err != nil {
		t.Fatalf("insert occurrence: %v", err)
	}
}

func preventionActiveAt(t *testing.T, conn *pgx.Conn, registration database.PreventionRegistration, activatedAt time.Time) database.PreventionRegistrationResult {
	t.Helper()
	registered := registerPrevention(t, conn, registration)
	if _, err := database.RecordPreventionVerification(context.Background(), conn, database.PreventionVerificationInput{
		ArtifactVersionID: registered.Version.ArtifactVersionID, Command: []string{"go", "test"},
		ContentSHA256: registration.ContentSHA256, Passed: true,
	}); err != nil {
		t.Fatalf("verify prevention fixture: %v", err)
	}
	activatePrevention(t, conn, registered.Version.ArtifactVersionID, preventionCommitA)
	if _, err := conn.Exec(context.Background(), `update prevention_artifact_versions set activated_at = $2 where id = $1`, registered.Version.ArtifactVersionID, activatedAt); err != nil {
		t.Fatalf("set prevention activation time: %v", err)
	}
	return registered
}

func TestPreventionStatsAtWindowsMaturityAndFilters(t *testing.T) {
	conn, _ := preventionSchema(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	firstActivation := now.Add(-45 * 24 * time.Hour)
	secondActivation := now.Add(-30 * 24 * time.Hour)
	firstIssue := preventionIssue(t, conn, "proj-a", "resolved")
	secondIssue := preventionIssue(t, conn, "proj-a", "resolved")
	if _, err := conn.Exec(ctx, `update issues set first_seen = $2 where id = $1`, firstIssue, firstActivation.Add(-36*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `update issues set first_seen = $2 where id = $1`, secondIssue, firstActivation.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	first := registerPrevention(t, conn, preventionRegistration(firstIssue))
	linked := preventionRegistration(secondIssue)
	registerPrevention(t, conn, linked)
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, true)
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, false)
	verifyPrevention(t, conn, first.Version.ArtifactVersionID, true)
	activatePrevention(t, conn, first.Version.ArtifactVersionID, preventionCommitA)
	changed := preventionRegistration(firstIssue)
	changed.ContentSHA256 = preventionHashB
	second := registerPrevention(t, conn, changed)
	if _, err := database.RecordPreventionVerification(ctx, conn, database.PreventionVerificationInput{
		ArtifactVersionID: second.Version.ArtifactVersionID, Command: []string{"go", "test"},
		ContentSHA256: preventionHashB, Passed: true,
	}); err != nil {
		t.Fatal(err)
	}
	activatePrevention(t, conn, second.Version.ArtifactVersionID, preventionCommitB)
	if _, err := conn.Exec(ctx, `update prevention_artifact_versions set activated_at = case id when $1 then $3::timestamptz when $2 then $4::timestamptz end where id in ($1, $2)`,
		first.Version.ArtifactVersionID, second.Version.ArtifactVersionID, firstActivation, secondActivation); err != nil {
		t.Fatal(err)
	}
	for _, occurredAt := range []time.Time{
		firstActivation.Add(-30 * 24 * time.Hour), firstActivation, firstActivation.Add(30 * 24 * time.Hour),
		secondActivation.Add(-30 * 24 * time.Hour), secondActivation.Add(-time.Hour), secondActivation,
		now, now.Add(time.Hour),
	} {
		preventionOccurrence(t, conn, firstIssue, occurredAt)
	}

	matureZeroIssue := preventionIssue(t, conn, "proj-b", "resolved")
	matureZeroRegistration := preventionRegistration(matureZeroIssue)
	matureZeroRegistration.ArtifactPath = "checks/mature.sh"
	matureZero := preventionActiveAt(t, conn, matureZeroRegistration, now.Add(-31*24*time.Hour))
	observingIssue := preventionIssue(t, conn, "proj-a", "resolved")
	observingRegistration := preventionRegistration(observingIssue)
	observingRegistration.ArtifactPath = "checks/observing.sh"
	preventionActiveAt(t, conn, observingRegistration, now.Add(-23*time.Hour))
	if _, err := conn.Exec(ctx, `update issues set first_seen = $2 where id = $1`, observingIssue, now.Add(-33*time.Hour)); err != nil {
		t.Fatal(err)
	}

	stats, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{}, now)
	if err != nil {
		t.Fatalf("prevention stats: %v", err)
	}
	if stats.VerificationRuns != 6 || stats.VerificationPassed != 5 || stats.VerificationPassRate != float64(5)/6 {
		t.Fatalf("verification stats = runs %d passed %d rate %v", stats.VerificationRuns, stats.VerificationPassed, stats.VerificationPassRate)
	}
	byStatus := map[string]int64{}
	for _, count := range stats.ByStatus {
		byStatus[count.Name] = count.Count
	}
	if len(byStatus) != 2 || byStatus["active"] != 3 || byStatus["deprecated"] != 1 {
		t.Fatalf("status counts = %+v", stats.ByStatus)
	}
	if len(stats.ByKind) != 1 || stats.ByKind[0].Name != "regression_test" || stats.ByKind[0].Count != 3 {
		t.Fatalf("kind counts = %+v", stats.ByKind)
	}
	if len(stats.Activations) != 6 || stats.MatureActivations != 5 || stats.MatureZeroRecurrence != 3 || stats.MatureProtectionRate == nil || *stats.MatureProtectionRate != float64(3)/5 {
		t.Fatalf("maturity stats = activations %d mature %d zero %d rate %v", len(stats.Activations), stats.MatureActivations, stats.MatureZeroRecurrence, stats.MatureProtectionRate)
	}
	if len(stats.RecurringVersionIDs) != 1 || stats.RecurringVersionIDs[0] != second.Version.ArtifactVersionID {
		t.Fatalf("recurring active versions = %v, want only %s", stats.RecurringVersionIDs, second.Version.ArtifactVersionID)
	}
	activation := func(versionID, issueID string) database.PreventionActivationStats {
		t.Helper()
		for _, item := range stats.Activations {
			if item.ArtifactVersionID == versionID && item.IssueID == issueID {
				return item
			}
		}
		t.Fatalf("missing activation %s/%s in %+v", versionID, issueID, stats.Activations)
		return database.PreventionActivationStats{}
	}
	oldFirst := activation(first.Version.ArtifactVersionID, firstIssue)
	if oldFirst.Status != "deprecated" || oldFirst.FirstSeenToActivationMS != int64((36*time.Hour)/time.Millisecond) ||
		oldFirst.BeforeCount != 2 || oldFirst.AfterCount != 3 || oldFirst.RecurrenceCount != 5 || oldFirst.BeforeDays != 1 || oldFirst.AfterDays != 30 || oldFirst.ObservationStatus != "mature" {
		t.Fatalf("old activation windows = %+v", oldFirst)
	}
	newFirst := activation(second.Version.ArtifactVersionID, firstIssue)
	if newFirst.BeforeCount != 3 || newFirst.AfterCount != 2 || newFirst.RecurrenceCount != 3 || newFirst.ObservationStatus != "mature" {
		t.Fatalf("new activation windows = %+v", newFirst)
	}
	oldSecond := activation(first.Version.ArtifactVersionID, secondIssue)
	if oldSecond.FirstSeenToActivationMS != 0 || oldSecond.BeforeDays != 0 || oldSecond.RecurrenceCount != 0 {
		t.Fatalf("second issue activation = %+v", oldSecond)
	}
	var observingStats database.PreventionActivationStats
	for _, item := range stats.Activations {
		if item.IssueID == observingIssue {
			observingStats = item
		}
	}
	if observingStats.BeforeDays != 0 || observingStats.AfterDays != 0 || observingStats.ObservationStatus != "observing" {
		t.Fatalf("observing activation = %+v", observingStats)
	}

	issueStats, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{IssueID: firstIssue}, now)
	if err != nil || issueStats.VerificationRuns != 4 || len(issueStats.Activations) != 2 || len(issueStats.ByKind) != 1 || issueStats.ByKind[0].Count != 1 || issueStats.MatureActivations != 2 || issueStats.MatureZeroRecurrence != 0 || issueStats.MatureProtectionRate == nil || *issueStats.MatureProtectionRate != 0 {
		t.Fatalf("issue-filtered stats = %+v, %v", issueStats, err)
	}
	secondIssueStats, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{IssueID: secondIssue}, now)
	if err != nil || len(secondIssueStats.Activations) != 2 || secondIssueStats.MatureZeroRecurrence != 2 || secondIssueStats.MatureProtectionRate == nil || *secondIssueStats.MatureProtectionRate != 1 || len(secondIssueStats.RecurringVersionIDs) != 0 {
		t.Fatalf("second issue stats = %+v, %v", secondIssueStats, err)
	}
	projectStats, err := database.PreventionStatsAt(ctx, conn, database.PreventionFilter{ProjectKey: "proj-b"}, now)
	if err != nil || projectStats.VerificationRuns != 1 || len(projectStats.Activations) != 1 || projectStats.Activations[0].ArtifactID != matureZero.Artifact.ArtifactID || projectStats.MatureProtectionRate == nil || *projectStats.MatureProtectionRate != 1 {
		t.Fatalf("project-filtered stats = %+v, %v", projectStats, err)
	}
}

func TestPreventionStatsEmptyUsesZeroRatesAndRequiredArrays(t *testing.T) {
	conn, _ := preventionSchema(t)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	stats, err := database.PreventionStatsAt(context.Background(), conn, database.PreventionFilter{ProjectKey: "missing"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ByStatus == nil || stats.ByKind == nil || stats.Activations == nil || stats.RecurringVersionIDs == nil || len(stats.ByStatus) != 0 || len(stats.Activations) != 0 {
		t.Fatalf("empty stats arrays = %+v", stats)
	}
	if stats.VerificationRuns != 0 || stats.VerificationPassRate != 0 || stats.MatureActivations != 0 || stats.MatureProtectionRate != nil {
		t.Fatalf("empty stats rates = %+v", stats)
	}
	current, err := database.PreventionStats(context.Background(), conn, database.PreventionFilter{})
	if err != nil || current.MatureProtectionRate != nil {
		t.Fatalf("current-time empty stats = %+v, %v", current, err)
	}
}
