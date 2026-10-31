//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

type preventionCLIClient struct {
	*fakeClient
	addRequest       contract.PreventionAddRequest
	verifyRequest    contract.PreventionVerifyRequest
	activateRequest  contract.PreventionActivateRequest
	listRequest      contract.PreventionListRequest
	getRequest       contract.PreventionGetRequest
	addResponse      *contract.PreventionAddResponse
	verifyResponse   *contract.PreventionVerifyResponse
	activateResponse *contract.PreventionActivateResponse
	listResponse     *contract.PreventionListResponse
	getResponse      *contract.PreventionGetResponse
	deadlines        []time.Duration
}

func newPreventionCLIClient() *preventionCLIClient {
	return &preventionCLIClient{fakeClient: &fakeClient{}}
}

func (f *preventionCLIClient) observeDeadline(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	if ok {
		f.deadlines = append(f.deadlines, time.Until(deadline))
	}
}

func (f *preventionCLIClient) PreventionAdd(ctx context.Context, request contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	f.note(contract.OperationPreventionAdd)
	f.observeDeadline(ctx)
	f.addRequest = request
	if f.err != nil {
		return contract.PreventionAddResponse{}, f.err
	}
	if f.addResponse != nil {
		return *f.addResponse, nil
	}
	return contract.PreventionAddResponse{SchemaVersion: "v1", Artifact: preventionCLIArtifact(), Version: preventionCLIVersion("draft"), Created: true}, nil
}

func (f *preventionCLIClient) PreventionVerify(ctx context.Context, request contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	f.note(contract.OperationPreventionVerify)
	f.observeDeadline(ctx)
	f.verifyRequest = request
	if f.err != nil {
		return contract.PreventionVerifyResponse{}, f.err
	}
	if f.verifyResponse != nil {
		return *f.verifyResponse, nil
	}
	return contract.PreventionVerifyResponse{SchemaVersion: "v1", Verification: preventionCLIVerification(true), Version: preventionCLIVersion("verified")}, nil
}

func (f *preventionCLIClient) PreventionActivate(ctx context.Context, request contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	f.note(contract.OperationPreventionActivate)
	f.observeDeadline(ctx)
	f.activateRequest = request
	if f.err != nil {
		return contract.PreventionActivateResponse{}, f.err
	}
	if f.activateResponse != nil {
		return *f.activateResponse, nil
	}
	return contract.PreventionActivateResponse{SchemaVersion: "v1", Version: preventionCLIVersion("active"), Activated: true}, nil
}

func (f *preventionCLIClient) PreventionList(ctx context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	f.note(contract.OperationPreventionList)
	f.observeDeadline(ctx)
	f.listRequest = request
	if f.err != nil {
		return contract.PreventionListResponse{}, f.err
	}
	if f.listResponse != nil {
		return *f.listResponse, nil
	}
	return contract.PreventionListResponse{SchemaVersion: "v1", Items: []contract.PreventionArtifactView{{Artifact: preventionCLIArtifact(), Versions: []contract.PreventionVersion{preventionCLIVersion("verified")}}}, NextCursor: "next-page"}, nil
}

func (f *preventionCLIClient) PreventionGet(ctx context.Context, request contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	f.note(contract.OperationPreventionGet)
	f.observeDeadline(ctx)
	f.getRequest = request
	if f.err != nil {
		return contract.PreventionGetResponse{}, f.err
	}
	if f.getResponse != nil {
		return *f.getResponse, nil
	}
	version := preventionCLIVersion("verified")
	verification := preventionCLIVerification(true)
	version.LatestVerification = &verification
	return contract.PreventionGetResponse{SchemaVersion: "v1", Artifact: preventionCLIArtifact(), Versions: []contract.PreventionVersion{version}, Verifications: []contract.PreventionVerification{verification}}, nil
}

func preventionCLITestEnv(fake *preventionCLIClient) (*cliEnv, *bytes.Buffer, *bytes.Buffer) {
	env, stdout, stderr := testEnv(fake.fakeClient)
	env.dial = func(context.Context) (apiClient, error) { return fake, nil }
	return env, stdout, stderr
}

func preventionCLIArtifact() contract.PreventionArtifact {
	return contract.PreventionArtifact{
		ArtifactID: "artifact-1", ProjectKey: "demo", Kind: "regression_test", Title: "Windows lock regression",
		IssueIDs: []string{"issue-1"}, CreatedAt: "2026-10-30T00:00:00Z", UpdatedAt: "2026-10-30T00:00:00Z",
	}
}

func preventionCLIVersion(status string) contract.PreventionVersion {
	version := contract.PreventionVersion{
		ArtifactVersionID: "version-1", ArtifactID: "artifact-1", Version: 1,
		ArtifactPath: "tests/windows_lock_test.go", ContentSHA256: strings.Repeat("a", 64),
		Status: status, CreatedAt: "2026-10-30T00:00:00Z",
	}
	if status == "active" {
		commit := strings.Repeat("b", 40)
		activated := "2026-10-30T00:01:00Z"
		version.CommitSHA = &commit
		version.ActivatedAt = &activated
	}
	return version
}

func preventionCLIVerification(passed bool) contract.PreventionVerification {
	exitCode := 0
	evidence := "verification passed"
	if !passed {
		exitCode = 7
		evidence = "assertion failed"
	}
	return contract.PreventionVerification{
		VerificationID: "verification-1", ArtifactVersionID: "version-1", Command: []string{"go", "test"},
		ExitCode: &exitCode, Passed: passed, DurationMS: 42, Evidence: evidence,
		Environment: map[string]string{"os": "windows"}, ContentSHA256: strings.Repeat("a", 64),
		CreatedAt: "2026-10-30T00:00:30Z",
	}
}

func TestPreventionUsageAndUnknownCommands(t *testing.T) {
	fake := newPreventionCLIClient()
	for _, args := range [][]string{nil, {"unknown"}, {"stats"}} {
		env, _, _ := preventionCLITestEnv(fake)
		if code := runCommand("prevention", args, env); code != exitUsage {
			t.Fatalf("prevention %v exit = %d, want %d", args, code, exitUsage)
		}
	}
	var usageText bytes.Buffer
	usage(&usageText)
	if !strings.Contains(usageText.String(), "prevention add|verify|activate|list|show") {
		t.Fatalf("usage does not list batch-three commands:\n%s", usageText.String())
	}
	if strings.Contains(usageText.String(), "prevention stats") {
		t.Fatalf("usage advertises prevention stats before batch five:\n%s", usageText.String())
	}
}

func TestPreventionAddSendsOnlyRelativePathAndHash(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "tests", "windows_lock_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte("package regression\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := newPreventionCLIClient()
	env, stdout, _ := preventionCLITestEnv(fake)
	env.json = true
	code := runCommand("prevention", []string{"add", "--issue", "issue-1", "--kind", "regression_test", "--title", "Windows lock regression", "--path", "tests/windows_lock_test.go", "--repo", repo}, env)
	if code != exitOK {
		t.Fatalf("prevention add exit = %d", code)
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256(body))
	request := fake.addRequest
	if request.IssueID != "issue-1" || request.ArtifactPath != "tests/windows_lock_test.go" || request.ContentSHA256 != wantHash {
		t.Fatalf("add request = %+v", request)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, body) || bytes.Contains(encoded, []byte(repo)) {
		t.Fatalf("add request leaked body or repository path: %s", encoded)
	}
	var response contract.PreventionAddResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("add JSON: %v\n%s", err, stdout.String())
	}
	if err := response.Validate(); err != nil || response.Version.Status != "draft" {
		t.Fatalf("add response = %+v, %v", response, err)
	}
	if len(fake.deadlines) != 1 || fake.deadlines[0] > cliTimeout || fake.deadlines[0] < cliTimeout-time.Second {
		t.Fatalf("add timeout = %v, want ordinary %s", fake.deadlines, cliTimeout)
	}
}

func TestPreventionAddRejectsUnsafeFilesBeforeDial(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "large.txt"), bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "link.txt")); err != nil {
		t.Logf("symlink fixture unavailable: %v", err)
	}
	tests := []struct{ name, path string }{
		{name: "parent traversal", path: "../outside.txt"},
		{name: "absolute", path: outside},
		{name: "too large", path: "large.txt"},
	}
	if _, err := os.Lstat(filepath.Join(repo, "link.txt")); err == nil {
		tests = append(tests, struct{ name, path string }{name: "symlink", path: "link.txt"})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := newPreventionCLIClient()
			dialed := false
			env, _, _ := preventionCLITestEnv(fake)
			env.dial = func(context.Context) (apiClient, error) {
				dialed = true
				return fake, nil
			}
			code := runCommand("prevention", []string{"add", "--issue", "issue-1", "--kind", "regression_test", "--title", "regression", "--repo", repo, "--path", test.path}, env)
			if code == exitOK || dialed {
				t.Fatalf("unsafe add exit=%d dialed=%v", code, dialed)
			}
		})
	}
}

func TestPreventionVerifyPreservesArgvAndUsesLongTimeout(t *testing.T) {
	fake := newPreventionCLIClient()
	env, stdout, _ := preventionCLITestEnv(fake)
	argv := []string{"tool with space", `quote"arg`, ";", "--json"}
	code := runCommand("prevention", []string{"verify", "version-1", "--repo", "repo path", "--", argv[0], argv[1], argv[2], argv[3]}, env)
	if code != exitOK {
		t.Fatalf("prevention verify exit = %d", code)
	}
	if !reflect.DeepEqual(fake.verifyRequest.Command, argv) {
		t.Fatalf("verify argv = %#v, want %#v", fake.verifyRequest.Command, argv)
	}
	if len(fake.deadlines) != 1 || fake.deadlines[0] > 125*time.Second || fake.deadlines[0] < 124*time.Second {
		t.Fatalf("verify timeout = %v, want 125s", fake.deadlines)
	}
	if !strings.Contains(stdout.String(), "prevention activate version-1 --repo <repository-path>") {
		t.Fatalf("verify success has no activation hint:\n%s", stdout.String())
	}
}

func TestPreventionVerifyGlobalJSONDoesNotRewriteCommand(t *testing.T) {
	fake := newPreventionCLIClient()
	originalDial := dialClient
	dialClient = func(context.Context) (apiClient, error) { return fake, nil }
	t.Cleanup(func() { dialClient = originalDial })
	var stdout, stderr bytes.Buffer
	code := run([]string{"prevention", "verify", "version-1", "--repo", "repo", "--json", "--", "tool", "--json"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("verify exit=%d stderr=%s", code, stderr.String())
	}
	if want := []string{"tool", "--json"}; !reflect.DeepEqual(fake.verifyRequest.Command, want) {
		t.Fatalf("verify argv=%#v, want %#v", fake.verifyRequest.Command, want)
	}
	var response contract.PreventionVerifyResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("verify JSON: %v\n%s", err, stdout.String())
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("verify response: %v", err)
	}
}

func TestPreventionVerifyFailureIsStoredAndReturnsOne(t *testing.T) {
	fake := newPreventionCLIClient()
	fake.verifyResponse = &contract.PreventionVerifyResponse{SchemaVersion: "v1", Verification: preventionCLIVerification(false), Version: preventionCLIVersion("draft")}
	env, stdout, _ := preventionCLITestEnv(fake)
	code := runCommand("prevention", []string{"verify", "--repo", "repo", "version-1", "--", "tool", "arg"}, env)
	if code != exitInternal {
		t.Fatalf("failed verification exit = %d, want 1", code)
	}
	for _, want := range []string{"verification-1", "passed=false", "exit=7", "assertion failed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("failed verification output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestPreventionActivateIsIdempotent(t *testing.T) {
	fake := newPreventionCLIClient()
	fake.activateResponse = &contract.PreventionActivateResponse{SchemaVersion: "v1", Version: preventionCLIVersion("active"), Activated: false}
	env, stdout, _ := preventionCLITestEnv(fake)
	code := runCommand("prevention", []string{"activate", "version-1", "--repo", "repo"}, env)
	if code != exitOK || fake.activateRequest.ArtifactVersionID != "version-1" || fake.activateRequest.RepoPath != "repo" {
		t.Fatalf("activate code=%d request=%+v", code, fake.activateRequest)
	}
	for _, want := range []string{"active", "activated=false", strings.Repeat("b", 40), "2026-10-30T00:01:00Z"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("activate output lacks %q:\n%s", want, stdout.String())
		}
	}
	if len(fake.deadlines) != 1 || fake.deadlines[0] > cliTimeout {
		t.Fatalf("activate used a long verification timeout: %v", fake.deadlines)
	}
}

func TestPreventionListFiltersAndShowHistory(t *testing.T) {
	fake := newPreventionCLIClient()
	env, stdout, _ := preventionCLITestEnv(fake)
	code := runCommand("prevention", []string{"list", "--issue", "issue-1", "--project", "demo", "--kind", "regression_test", "--status", "verified", "--limit", "5", "--cursor", "cursor-1"}, env)
	if code != exitOK || fake.listRequest.IssueID != "issue-1" || fake.listRequest.ProjectKey != "demo" || fake.listRequest.Limit == nil || *fake.listRequest.Limit != 5 {
		t.Fatalf("list code=%d request=%+v", code, fake.listRequest)
	}
	for _, want := range []string{"artifact-1", "verified", "next-page"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("list output lacks %q:\n%s", want, stdout.String())
		}
	}

	env, stdout, _ = preventionCLITestEnv(fake)
	code = runCommand("prevention", []string{"show", "artifact-1"}, env)
	if code != exitOK || fake.getRequest.ArtifactID != "artifact-1" {
		t.Fatalf("show code=%d request=%+v", code, fake.getRequest)
	}
	for _, want := range []string{"Windows lock regression", "version-1", "verification-1", "verification passed", "prevention activate version-1 --repo <repository-path>"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("show output lacks %q:\n%s", want, stdout.String())
		}
	}
}

func TestPreventionListEmptyAndShowNotFound(t *testing.T) {
	fake := newPreventionCLIClient()
	fake.listResponse = &contract.PreventionListResponse{SchemaVersion: "v1", Items: []contract.PreventionArtifactView{}}
	env, stdout, _ := preventionCLITestEnv(fake)
	if code := runCommand("prevention", []string{"list"}, env); code != exitOK || !strings.Contains(stdout.String(), "no preventions") {
		t.Fatalf("empty list code=%d output=%s", code, stdout.String())
	}
	fake.err = &service.CallError{Code: service.CodeNotFound, Message: "missing prevention"}
	env, _, _ = preventionCLITestEnv(fake)
	if code := runCommand("prevention", []string{"show", "missing"}, env); code != exitNotFound {
		t.Fatalf("missing show exit=%d, want 3", code)
	}
}

func TestPreventionUnavailableAndArgumentErrors(t *testing.T) {
	fake := newPreventionCLIClient()
	fake.err = service.ErrServiceUnavailable
	env, _, _ := preventionCLITestEnv(fake)
	if code := runCommand("prevention", []string{"activate", "version-1", "--repo", "repo"}, env); code != exitUnavailable {
		t.Fatalf("unavailable activate exit=%d, want 4", code)
	}
	fake.err = nil
	env, _, _ = preventionCLITestEnv(fake)
	if code := runCommand("prevention", []string{"verify", "version-1", "--repo", "repo"}, env); code != exitUsage {
		t.Fatalf("verify without command exit=%d, want 2", code)
	}
	env, _, _ = preventionCLITestEnv(fake)
	if code := runCommand("prevention", []string{"add", "--issue", "issue-1"}, env); code != exitUsage {
		t.Fatalf("incomplete add exit=%d, want 2", code)
	}
}
