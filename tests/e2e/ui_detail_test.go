//go:build windows

// The EKB-058 acceptance record: the detail screen reads one issue with its
// history, appends a solution version, changes an issue status, and can only
// delete with an explicit confirmation. Every write leaves an audit row.
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

func issueIDFromRun(t *testing.T, output string) string {
	t.Helper()
	marker := "failure recorded as issue "
	index := strings.Index(output, marker)
	if index < 0 {
		t.Fatalf("the run output carries no issue id:\n%s", output)
	}
	rest := output[index+len(marker):]
	if end := strings.IndexAny(rest, " (\r\n"); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func countAudit(t *testing.T, conn *pgx.Conn, action string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(),
		"select count(*) from audit_log where action = $1", action).Scan(&count); err != nil {
		t.Fatalf("count audit action %s: %v", action, err)
	}
	return count
}

func TestToolboxDetailAndSolutionEditing(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the real toolbox acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := fmt.Sprintf("errkb_uidetail_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create the acceptance schema: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	}()
	scopedDSN := withSearchPath(dsn, schema)
	conn, err := pgx.Connect(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("connect to the acceptance schema: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := database.Migrate(ctx, conn, migrationsDir(t)); err != nil {
		t.Fatalf("migrate the acceptance schema: %v", err)
	}

	var connectorID, sessionID string
	if err := conn.QueryRow(ctx, `insert into connectors (name, version, channel, capabilities, source, actor)
		values ('e2e-detail', '0.0.0', 'B', '{}'::jsonb, 'e2e', 'e2e') returning id::text`).Scan(&connectorID); err != nil {
		t.Fatalf("seed the connector: %v", err)
	}
	if err := conn.QueryRow(ctx, `insert into sessions (connector_id, external_id, started_at, source, actor)
		values ($1, 'e2e-detail-session', now(), 'e2e', 'e2e') returning id::text`, connectorID).Scan(&sessionID); err != nil {
		t.Fatalf("seed the session: %v", err)
	}

	binary := buildBinary(t)
	pipeName := fmt.Sprintf(`\\.\pipe\errkb-detail-e2e-%d`, time.Now().UnixNano())
	lockName := fmt.Sprintf(`Local\errkb-detail-e2e-%d`, time.Now().UnixNano())
	dataDir := t.TempDir()
	startDaemon(t, binary, scopedDSN, pipeName, lockName, dataDir)

	output, code := runCLI(t, binary, pipeName, "run", "--connector", connectorID, "--session", sessionID,
		"--project", e2eProject, "--", "cmd", "/c", "exit 5")
	if code != 5 {
		t.Fatalf("the recording run exited %d, want 5:\n%s", code, output)
	}
	issueID := issueIDFromRun(t, output)

	var solutionID string
	if err := conn.QueryRow(ctx, `insert into solutions (issue_id, status, source, actor)
		values ($1, 'candidate', 'e2e', 'e2e') returning id::text`, issueID).Scan(&solutionID); err != nil {
		t.Fatalf("seed the solution: %v", err)
	}
	if _, err := database.AddSolutionVersion(ctx, conn, solutionID, "restart the service", "",
		"", database.Environment{}, "e2e", "e2e"); err != nil {
		t.Fatalf("seed the solution version: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err != nil {
		t.Fatalf("verify the solution: %v", err)
	}
	if _, err := conn.Exec(ctx, `update issues set status = 'resolved' where id = $1`, issueID); err != nil {
		t.Fatalf("resolve the prevention issue fixture: %v", err)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "checks"), 0o700); err != nil {
		t.Fatalf("create the prevention fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "checks", "regression.txt"), []byte("read-only prevention fixture\n"), 0o600); err != nil {
		t.Fatalf("write the prevention fixture: %v", err)
	}
	addOutput, addCode := runCLI(t, binary, pipeName, "prevention", "add",
		"--issue", issueID, "--kind", "regression_test", "--title", "Locked file regression",
		"--repo", repo, "--path", "checks/regression.txt", "--json")
	var added contract.PreventionAddResponse
	if addCode != 0 || json.Unmarshal([]byte(addOutput), &added) != nil {
		t.Fatalf("prevention add exit=%d response=%+v output=%s", addCode, added, addOutput)
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	pageURL, stop := startToolbox(t, binary, scopedDSN, configPath, pipeName)
	defer stop()
	token := lastToken(t, pageURL)
	base := strings.TrimSuffix(pageURL, "/?token="+token)

	deadline := time.Now().Add(45 * time.Second)
	for {
		if _, status := callJSON(t, http.MethodGet, base+"/api/issues/"+issueID, token, nil); status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the toolbox never answered the detail route")
		}
		time.Sleep(300 * time.Millisecond)
	}

	payload, status := callJSON(t, http.MethodGet, base+"/api/issues/"+issueID, token, nil)
	if status != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", status, payload)
	}
	var detail struct {
		Issue struct {
			ID    string `json:"ID"`
			Title string `json:"Title"`
			Trust struct {
				Samples int `json:"Samples"`
			} `json:"Trust"`
			Occurrences []struct {
				Headline string `json:"Headline"`
			} `json:"Occurrences"`
			Solutions []struct {
				ID       string `json:"ID"`
				Versions []struct {
					Body   string `json:"Body"`
					Source string `json:"Source"`
				} `json:"Versions"`
			} `json:"Solutions"`
		} `json:"Issue"`
	}
	if err := json.Unmarshal(payload, &detail); err != nil {
		t.Fatalf("decode the detail: %v", err)
	}
	if detail.Issue.ID != issueID || len(detail.Issue.Occurrences) == 0 || len(detail.Issue.Solutions) == 0 {
		t.Fatalf("the detail page is incomplete: %+v", detail.Issue)
	}
	if len(detail.Issue.Solutions[0].Versions) == 0 || detail.Issue.Solutions[0].Versions[0].Body != "restart the service" {
		t.Fatalf("the version history is missing: %+v", detail.Issue.Solutions[0].Versions)
	}

	// Preventions use the same daemon client as the detail read and remain
	// strictly read-only in the Web shell.
	payload, status = callJSON(t, http.MethodGet, base+"/api/issues/"+issueID+"/preventions", token, nil)
	var preventions contract.PreventionListResponse
	if status != http.StatusOK || json.Unmarshal(payload, &preventions) != nil {
		t.Fatalf("preventions status=%d response=%+v body=%s", status, preventions, payload)
	}
	if err := preventions.Validate(); err != nil || len(preventions.Items) != 1 || len(preventions.Items[0].Versions) != 1 {
		t.Fatalf("prevention response=%+v err=%v", preventions, err)
	}
	if preventions.Items[0].Artifact.ArtifactID != added.Artifact.ArtifactID ||
		preventions.Items[0].Artifact.Title != "Locked file regression" ||
		preventions.Items[0].Versions[0].ArtifactVersionID != added.Version.ArtifactVersionID ||
		preventions.Items[0].Versions[0].Status != "draft" {
		t.Fatalf("wrong prevention projection: %+v", preventions.Items[0])
	}
	if _, status = callJSON(t, http.MethodPost, base+"/api/issues/"+issueID+"/preventions", token,
		strings.NewReader(`{"status":"active"}`)); status != http.StatusMethodNotAllowed {
		t.Fatalf("prevention Web write status=%d, want 405", status)
	}

	// Append a version: audited, and the issue is resolved by the write path.
	beforeVersions := countRows(t, conn, "select count(*) from solution_versions")
	payload, status = callJSON(t, http.MethodPost, base+"/api/issues/"+issueID+"/versions", token,
		strings.NewReader(`{"body":"clear the cache","preconditions":"none","source":"e2e-toolbox"}`))
	if status != http.StatusOK {
		t.Fatalf("append version status = %d, body = %s", status, payload)
	}
	if after := countRows(t, conn, "select count(*) from solution_versions"); after != beforeVersions+1 {
		t.Fatalf("solution_versions = %d, want %d", after, beforeVersions+1)
	}
	if countAudit(t, conn, "solution.version.added") != 1 {
		t.Fatal("appending a version must leave a solution.version.added audit row")
	}

	// Change the issue status: audited.
	payload, status = callJSON(t, http.MethodPost, base+"/api/issues/"+issueID+"/status", token,
		strings.NewReader(`{"status":"wontfix"}`))
	if status != http.StatusOK {
		t.Fatalf("issue status status = %d, body = %s", status, payload)
	}
	var storedStatus string
	if err := conn.QueryRow(ctx, "select status from issues where id = $1", issueID).Scan(&storedStatus); err != nil {
		t.Fatalf("read the issue status: %v", err)
	}
	if storedStatus != "wontfix" {
		t.Fatalf("issue status = %q, want wontfix", storedStatus)
	}
	if countAudit(t, conn, "issue.status.changed") != 1 {
		t.Fatal("changing an issue status must leave an issue.status.changed audit row")
	}

	// A destructive solution transition needs the explicit confirmation flag.
	if _, status = callJSON(t, http.MethodPost, base+"/api/solutions/"+solutionID+"/status", token,
		strings.NewReader(`{"status":"blocked"}`)); status != http.StatusConflict {
		t.Fatalf("unconfirmed status transition = %d, want 409", status)
	}
	if _, status = callJSON(t, http.MethodPost, base+"/api/solutions/"+solutionID+"/status", token,
		strings.NewReader(`{"status":"blocked","confirmed":true}`)); status != http.StatusOK {
		t.Fatalf("confirmed status transition = %d, want 200", status)
	}
	if countAudit(t, conn, "solution.status.changed") != 1 {
		t.Fatal("a solution transition must leave a solution.status.changed audit row")
	}

	// Deletion is refused without confirmation and audited with it.
	if _, status = callJSON(t, http.MethodPost, base+"/api/solutions/"+solutionID+"/delete", token,
		strings.NewReader(`{}`)); status != http.StatusConflict {
		t.Fatalf("unconfirmed delete = %d, want 409", status)
	}
	if countRows(t, conn, "select count(*) from solutions") != 1 {
		t.Fatal("an unconfirmed delete must not write anything")
	}
	if _, status = callJSON(t, http.MethodPost, base+"/api/solutions/"+solutionID+"/delete", token,
		strings.NewReader(`{"confirmed":true}`)); status != http.StatusOK {
		t.Fatalf("confirmed delete = %d, want 200", status)
	}
	if countRows(t, conn, "select count(*) from solutions") != 0 {
		t.Fatal("the confirmed delete must remove the solution")
	}
	if countAudit(t, conn, "solution.deleted") != 1 {
		t.Fatal("the confirmed delete must leave a solution.deleted audit row")
	}

	if _, status = callJSON(t, http.MethodGet, base+"/api/issues/does-not-exist", token, nil); status != http.StatusNotFound {
		t.Fatalf("unknown issue status = %d, want 404", status)
	}
}
