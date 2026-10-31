//go:build windows

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// TestCLIPreventionHelper is re-executed as the explicit verification command.
// The daemon inherits the helper mode from the test environment; ordinary test
// runs return immediately and never sleep.
func TestCLIPreventionHelper(t *testing.T) {
	if os.Getenv("ERRKB_PREVENTION_E2E_HELPER") != "timeout" {
		return
	}
	time.Sleep(130 * time.Second)
	os.Exit(0)
}

func TestCLIPreventionLoop(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the real prevention loop")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("Git is required for the real prevention loop: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	schema := fmt.Sprintf("errkb_prevention_e2e_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create the prevention schema: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	}()
	scopedDSN := withSearchPath(dsn, schema)
	conn, err := pgx.Connect(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("connect to the prevention schema: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := database.Migrate(ctx, conn, migrationsDir(t)); err != nil {
		t.Fatalf("migrate the prevention schema: %v", err)
	}

	// The helper is deterministic and local. It exercises the real 120-second
	// execution deadline rather than mocking time or changing production limits.
	t.Setenv("ERRKB_PREVENTION_E2E_HELPER", "timeout")
	binary := buildBinary(t)
	dataDir := t.TempDir()
	pipeName := fmt.Sprintf(`\\.\pipe\errkb-prevention-e2e-%d`, time.Now().UnixNano())
	lockName := fmt.Sprintf(`Local\errkb-prevention-e2e-%d`, time.Now().UnixNano())
	startDaemon(t, binary, scopedDSN, pipeName, lockName, dataDir)

	failureArgs := func() []string {
		return []string{"run", "--project", "prevention-e2e", "--json", "--", "cmd", "/c", "exit 9"}
	}
	firstOutput, firstCode := runCLI(t, binary, pipeName, failureArgs()...)
	if firstCode == 0 {
		t.Fatalf("the recorded command must fail:\n%s", firstOutput)
	}
	var first commandRun
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil || first.Record == nil {
		t.Fatalf("decode the first failure (exit %d): %v\n%s", firstCode, err, firstOutput)
	}
	issueID := first.Record.IssueID
	if _, err := conn.Exec(ctx, `update issues set status = 'resolved' where id = $1`, issueID); err != nil {
		t.Fatalf("resolve the prevention issue: %v", err)
	}

	repo := t.TempDir()
	artifactBody := "regression fixture: preserve the locked-file retry boundary\n"
	artifactPath := filepath.Join(repo, "checks", "regression.txt")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte(artifactBody), 0o600); err != nil {
		t.Fatal(err)
	}
	preventionGit(t, repo, "init")
	preventionGit(t, repo, "config", "user.name", "ERRKB Test")
	preventionGit(t, repo, "config", "user.email", "errkb-test@example.invalid")
	preventionGit(t, repo, "add", "--", "checks/regression.txt")
	preventionGit(t, repo, "commit", "-m", "test: add prevention fixture")
	initialHead := preventionGit(t, repo, "rev-parse", "HEAD")
	assertPreventionGitState(t, repo, initialHead, "")

	addOutput, addCode := runCLI(t, binary, pipeName, "prevention", "add",
		"--issue", issueID, "--kind", "regression_test", "--title", "Locked file regression",
		"--repo", repo, "--path", "checks/regression.txt", "--json")
	if addCode != 0 {
		t.Fatalf("prevention add exit=%d:\n%s", addCode, addOutput)
	}
	var added contract.PreventionAddResponse
	if err := json.Unmarshal([]byte(addOutput), &added); err != nil {
		t.Fatalf("decode prevention add: %v\n%s", err, addOutput)
	}
	if err := added.Validate(); err != nil || !added.Created || added.Version.Status != "draft" {
		t.Fatalf("prevention add response=%+v err=%v", added, err)
	}
	assertPreventionGitState(t, repo, initialHead, "")
	if got := countRows(t, conn, `select count(*) from prevention_artifacts where title like '%' || $1 || '%' or title like '%' || $2 || '%'`, repo, artifactBody); got != 0 {
		t.Fatalf("artifact metadata leaked the body or repository path")
	}
	if got := countRows(t, conn, `select count(*) from audit_log where action like 'prevention.%' and detail::text like '%' || $1 || '%'`, artifactBody); got != 0 {
		t.Fatalf("prevention audit leaked the artifact body")
	}

	verifyOutput, verifyCode := runCLI(t, binary, pipeName, "prevention", "verify", added.Version.ArtifactVersionID,
		"--repo", repo, "--json", "--", "cmd", "/c", "echo verification passed")
	if verifyCode != 0 {
		t.Fatalf("prevention verify exit=%d:\n%s", verifyCode, verifyOutput)
	}
	var verified contract.PreventionVerifyResponse
	if err := json.Unmarshal([]byte(verifyOutput), &verified); err != nil {
		t.Fatalf("decode prevention verify: %v\n%s", err, verifyOutput)
	}
	if !verified.Verification.Passed || verified.Version.Status != "verified" {
		t.Fatalf("verification response=%+v", verified)
	}
	if got := countRows(t, conn, `select count(*) from prevention_verifications where artifact_version_id = $1 and passed`, added.Version.ArtifactVersionID); got != 1 {
		t.Fatalf("successful verification rows=%d, want 1", got)
	}
	if got := countRows(t, conn, `select count(*) from audit_log where action = 'prevention.verify' and entity_id = $1`, added.Version.ArtifactVersionID); got != 1 {
		t.Fatalf("successful verification audits=%d, want 1", got)
	}
	assertPreventionGitState(t, repo, initialHead, "")

	activateOutput, activateCode := runCLI(t, binary, pipeName, "prevention", "activate", added.Version.ArtifactVersionID, "--repo", repo, "--json")
	if activateCode != 0 {
		t.Fatalf("prevention activate exit=%d:\n%s", activateCode, activateOutput)
	}
	var activated contract.PreventionActivateResponse
	if err := json.Unmarshal([]byte(activateOutput), &activated); err != nil {
		t.Fatalf("decode prevention activate: %v\n%s", err, activateOutput)
	}
	if !activated.Activated || activated.Version.Status != "active" || activated.Version.CommitSHA == nil || *activated.Version.CommitSHA != initialHead {
		t.Fatalf("activation response=%+v, HEAD=%s", activated, initialHead)
	}
	activationAudits := countRows(t, conn, `select count(*) from audit_log where action = 'prevention.activate' and entity_id = $1`, added.Version.ArtifactVersionID)
	retryOutput, retryCode := runCLI(t, binary, pipeName, "prevention", "activate", added.Version.ArtifactVersionID, "--repo", repo, "--json")
	var retry contract.PreventionActivateResponse
	if retryCode != 0 || json.Unmarshal([]byte(retryOutput), &retry) != nil || retry.Activated || retry.Version.Status != "active" {
		t.Fatalf("idempotent activation exit=%d response=%+v output=%s", retryCode, retry, retryOutput)
	}
	if got := countRows(t, conn, `select count(*) from audit_log where action = 'prevention.activate' and entity_id = $1`, added.Version.ArtifactVersionID); got != activationAudits {
		t.Fatalf("idempotent activation changed audits from %d to %d", activationAudits, got)
	}
	assertPreventionGitState(t, repo, initialHead, "")

	if err := os.WriteFile(artifactPath, []byte(artifactBody+"uncommitted change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirtyStatus := preventionGit(t, repo, "status", "--porcelain=v1")
	if dirtyOutput, dirtyCode := runCLI(t, binary, pipeName, "prevention", "activate", added.Version.ArtifactVersionID, "--repo", repo, "--json"); dirtyCode == 0 {
		t.Fatalf("dirty activation unexpectedly succeeded:\n%s", dirtyOutput)
	}
	assertPreventionGitState(t, repo, initialHead, dirtyStatus)
	assertPreventionVersion(t, conn, added.Version.ArtifactVersionID, "active", initialHead)
	if got := countRows(t, conn, `select count(*) from audit_log where action = 'prevention.activate' and entity_id = $1`, added.Version.ArtifactVersionID); got != activationAudits {
		t.Fatalf("dirty activation added an audit row")
	}
	if err := os.WriteFile(artifactPath, []byte(artifactBody), 0o600); err != nil {
		t.Fatal(err)
	}
	assertPreventionGitState(t, repo, initialHead, "")

	secondOutput, secondCode := runCLI(t, binary, pipeName, failureArgs()...)
	var second commandRun
	if secondCode == 0 || json.Unmarshal([]byte(secondOutput), &second) != nil || second.Record == nil || second.Record.IssueID != issueID {
		t.Fatalf("recurrence did not reuse the issue: exit=%d response=%+v output=%s", secondCode, second, secondOutput)
	}
	if got := countRows(t, conn, `select count(*) from occurrences where issue_id = $1`, issueID); got != 2 {
		t.Fatalf("issue occurrences after recurrence=%d, want 2", got)
	}
	if got := countRows(t, conn, `select count(*) from occurrences where issue_id = $1 and created_at > (select activated_at from prevention_artifact_versions where id = $2)`, issueID, added.Version.ArtifactVersionID); got != 1 {
		t.Fatalf("post-activation recurrence rows=%d, want 1", got)
	}
	assertPreventionGitState(t, repo, initialHead, "")

	// Active versions cannot be re-verified. Register a committed v2 to prove
	// that failed and timed-out runs are appended and block its activation while
	// leaving the already-active v1 untouched.
	secondBody := artifactBody + "second verified boundary\n"
	if err := os.WriteFile(artifactPath, []byte(secondBody), 0o600); err != nil {
		t.Fatal(err)
	}
	preventionGit(t, repo, "add", "--", "checks/regression.txt")
	preventionGit(t, repo, "commit", "-m", "test: revise prevention fixture")
	secondHead := preventionGit(t, repo, "rev-parse", "HEAD")
	secondAddOutput, secondAddCode := runCLI(t, binary, pipeName, "prevention", "add",
		"--issue", issueID, "--kind", "regression_test", "--title", "Locked file regression",
		"--repo", repo, "--path", "checks/regression.txt", "--json")
	var secondAdded contract.PreventionAddResponse
	if secondAddCode != 0 || json.Unmarshal([]byte(secondAddOutput), &secondAdded) != nil || secondAdded.Version.Version != 2 || secondAdded.Version.Status != "draft" {
		t.Fatalf("second registration exit=%d response=%+v output=%s", secondAddCode, secondAdded, secondAddOutput)
	}

	secret := "sk-" + "abcdefghijklmnopqrstuvwx"
	failedOutput, failedCode := runCLI(t, binary, pipeName, "prevention", "verify", secondAdded.Version.ArtifactVersionID,
		"--repo", repo, "--json", "--", "cmd", "/c", "echo auth failed for "+secret+" 1>&2 & exit 7")
	var failed contract.PreventionVerifyResponse
	if failedCode != 1 || json.Unmarshal([]byte(failedOutput), &failed) != nil || failed.Verification.Passed || failed.Verification.ExitCode == nil || *failed.Verification.ExitCode != 7 {
		t.Fatalf("failed verification exit=%d response=%+v output=%s", failedCode, failed, failedOutput)
	}
	if strings.Contains(failed.Verification.Evidence, secret) || !strings.Contains(failed.Verification.Evidence, "<secret>") {
		t.Fatalf("failed evidence was not redacted: %q", failed.Verification.Evidence)
	}
	if blockedOutput, blockedCode := runCLI(t, binary, pipeName, "prevention", "activate", secondAdded.Version.ArtifactVersionID, "--repo", repo, "--json"); blockedCode == 0 {
		t.Fatalf("activation succeeded after a failed verification:\n%s", blockedOutput)
	}

	helper, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the verification helper: %v", err)
	}
	timeoutOutput, timeoutCode := runCLI(t, binary, pipeName, "prevention", "verify", secondAdded.Version.ArtifactVersionID,
		"--repo", repo, "--json", "--", helper, "-test.run=^TestCLIPreventionHelper$")
	var timedOut contract.PreventionVerifyResponse
	if timeoutCode != 1 || json.Unmarshal([]byte(timeoutOutput), &timedOut) != nil || timedOut.Verification.Passed || timedOut.Verification.ExitCode == nil || *timedOut.Verification.ExitCode != 1 || !strings.Contains(timedOut.Verification.Evidence, "timed out") {
		t.Fatalf("timeout verification exit=%d response=%+v output=%s", timeoutCode, timedOut, timeoutOutput)
	}
	if blockedOutput, blockedCode := runCLI(t, binary, pipeName, "prevention", "activate", secondAdded.Version.ArtifactVersionID, "--repo", repo, "--json"); blockedCode == 0 {
		t.Fatalf("activation succeeded after a timeout:\n%s", blockedOutput)
	}
	if got := countRows(t, conn, `select count(*) from prevention_verifications where artifact_version_id = $1 and not passed`, secondAdded.Version.ArtifactVersionID); got != 2 {
		t.Fatalf("failed verification history=%d, want 2", got)
	}
	assertPreventionVersion(t, conn, added.Version.ArtifactVersionID, "active", initialHead)
	assertPreventionVersion(t, conn, secondAdded.Version.ArtifactVersionID, "draft", "")
	assertPreventionGitState(t, repo, secondHead, "")

	showOutput, showCode := runCLI(t, binary, pipeName, "prevention", "show", added.Artifact.ArtifactID, "--json")
	var shown contract.PreventionGetResponse
	if showCode != 0 || json.Unmarshal([]byte(showOutput), &shown) != nil || len(shown.Versions) != 2 || len(shown.Verifications) != 3 {
		t.Fatalf("prevention show exit=%d response=%+v output=%s", showCode, shown, showOutput)
	}
	listOutput, listCode := runCLI(t, binary, pipeName, "prevention", "list", "--issue", issueID, "--status", "active", "--json")
	var listed contract.PreventionListResponse
	if listCode != 0 || json.Unmarshal([]byte(listOutput), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("prevention list exit=%d response=%+v output=%s", listCode, listed, listOutput)
	}
}

func preventionGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func assertPreventionGitState(t *testing.T, repo, wantHead, wantStatus string) {
	t.Helper()
	if got := preventionGit(t, repo, "rev-parse", "HEAD"); got != wantHead {
		t.Fatalf("Git HEAD changed from %s to %s", wantHead, got)
	}
	if got := preventionGit(t, repo, "status", "--porcelain=v1"); got != wantStatus {
		t.Fatalf("Git status=%q, want %q", got, wantStatus)
	}
}

func assertPreventionVersion(t *testing.T, conn *pgx.Conn, versionID, wantStatus, wantCommit string) {
	t.Helper()
	var status string
	var commit *string
	if err := conn.QueryRow(context.Background(), `select status, commit_sha from prevention_artifact_versions where id = $1`, versionID).Scan(&status, &commit); err != nil {
		t.Fatalf("read prevention version: %v", err)
	}
	if status != wantStatus {
		t.Fatalf("version %s status=%s, want %s", versionID, status, wantStatus)
	}
	if wantCommit == "" {
		if commit != nil {
			t.Fatalf("version %s unexpectedly has commit %s", versionID, *commit)
		}
	} else if commit == nil || *commit != wantCommit {
		t.Fatalf("version %s commit=%v, want %s", versionID, commit, wantCommit)
	}
}
