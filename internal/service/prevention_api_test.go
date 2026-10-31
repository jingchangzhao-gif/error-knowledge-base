package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

type preventionAPIBackend struct {
	Backend
	operations []string
}

func (b *preventionAPIBackend) PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	b.operations = append(b.operations, contract.OperationPreventionAdd)
	return contract.PreventionAddResponse{SchemaVersion: "v1", Artifact: preventionContractArtifact(), Version: preventionContractVersion("draft"), Created: true}, nil
}

func (b *preventionAPIBackend) PreventionVerify(context.Context, contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	b.operations = append(b.operations, contract.OperationPreventionVerify)
	return contract.PreventionVerifyResponse{SchemaVersion: "v1", Verification: preventionContractVerification(), Version: preventionContractVersion("verified")}, nil
}

func (b *preventionAPIBackend) PreventionActivate(context.Context, contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	b.operations = append(b.operations, contract.OperationPreventionActivate)
	return contract.PreventionActivateResponse{SchemaVersion: "v1", Version: preventionContractVersion("active"), Activated: true}, nil
}

func (b *preventionAPIBackend) PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	b.operations = append(b.operations, contract.OperationPreventionList)
	return contract.PreventionListResponse{SchemaVersion: "v1", Items: []contract.PreventionArtifactView{}}, nil
}

func (b *preventionAPIBackend) PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	b.operations = append(b.operations, contract.OperationPreventionGet)
	return contract.PreventionGetResponse{SchemaVersion: "v1", Artifact: preventionContractArtifact(), Versions: []contract.PreventionVersion{}, Verifications: []contract.PreventionVerification{}}, nil
}

func preventionContractArtifact() contract.PreventionArtifact {
	return contract.PreventionArtifact{
		ArtifactID: "artifact-1", ProjectKey: "proj-a", Kind: "regression_test", Title: "regression",
		IssueIDs: []string{"issue-1"}, CreatedAt: "2026-10-30T00:00:00Z", UpdatedAt: "2026-10-30T00:00:00Z",
	}
}

func preventionContractVersion(status string) contract.PreventionVersion {
	version := contract.PreventionVersion{
		ArtifactVersionID: "version-1", ArtifactID: "artifact-1", Version: 1,
		ArtifactPath: "artifact.txt", ContentSHA256: strings.Repeat("a", 64), Status: status,
		CreatedAt: "2026-10-30T00:00:00Z",
	}
	if status == "active" {
		commit := strings.Repeat("1", 40)
		activated := "2026-10-30T00:01:00Z"
		version.CommitSHA = &commit
		version.ActivatedAt = &activated
	}
	return version
}

func preventionContractVerification() contract.PreventionVerification {
	exitCode := 0
	return contract.PreventionVerification{
		VerificationID: "verification-1", ArtifactVersionID: "version-1", Command: []string{"go", "test"},
		ExitCode: &exitCode, Passed: true, DurationMS: 1, Evidence: "ok",
		Environment: map[string]string{"os": "windows"}, ContentSHA256: strings.Repeat("a", 64),
		CreatedAt: "2026-10-30T00:00:30Z",
	}
}

func callPreventionAPI(t *testing.T, api *API, id, operation string, payload any) contract.PipeResponse {
	t.Helper()
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := json.Marshal(contract.PipeRequest{SchemaVersion: "v1", RequestID: id, Operation: operation, Payload: encodedPayload})
	if err != nil {
		t.Fatal(err)
	}
	response, err := contract.DecodePipeResponse(api.Handle(context.Background(), frame))
	if err != nil {
		t.Fatalf("decode %s response: %v", operation, err)
	}
	return response
}

func TestPreventionAPIRoutesExactlyFiveCoreOperations(t *testing.T) {
	backend := &preventionAPIBackend{}
	api := NewAPI(backend)
	add := contract.PreventionAddRequest{SchemaVersion: "v1", IssueID: "issue-1", Kind: "regression_test", Title: "regression", ArtifactPath: "artifact.txt", ContentSHA256: strings.Repeat("a", 64)}
	verify := contract.PreventionVerifyRequest{SchemaVersion: "v1", ArtifactVersionID: "version-1", RepoPath: "C:/repo", Command: []string{"go", "test"}}
	activate := contract.PreventionActivateRequest{SchemaVersion: "v1", ArtifactVersionID: "version-1", RepoPath: "C:/repo"}
	list := contract.PreventionListRequest{SchemaVersion: "v1"}
	get := contract.PreventionGetRequest{SchemaVersion: "v1", ArtifactID: "artifact-1"}
	for _, test := range []struct {
		operation string
		payload   any
	}{
		{contract.OperationPreventionAdd, add}, {contract.OperationPreventionVerify, verify},
		{contract.OperationPreventionActivate, activate}, {contract.OperationPreventionList, list},
		{contract.OperationPreventionGet, get},
	} {
		response := callPreventionAPI(t, api, "id-"+test.operation, test.operation, test.payload)
		if response.Error != nil || len(response.Result) == 0 {
			t.Fatalf("%s response = %+v", test.operation, response)
		}
	}
	if got := strings.Join(backend.operations, ","); got != "prevention_add,prevention_verify,prevention_activate,prevention_list,prevention_get" {
		t.Fatalf("prevention operations = %s", got)
	}
}

func TestSQLBackendPreventionLifecycle(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	var issueID string
	if err := conn.QueryRow(context.Background(), `
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		values ('abcdefabcdef', 'proj-a', 'resolved prevention issue', 'tool', 'resolved', '', 1, 'test', 'test') returning id
	`).Scan(&issueID); err != nil {
		t.Fatalf("insert resolved issue: %v", err)
	}
	repo := initPreventionGitRepo(t, "clean")
	hash, err := HashPreventionArtifact(repo, "artifact.txt")
	if err != nil {
		t.Fatal(err)
	}
	backend := NewSQLBackend(pool)
	ctx := context.Background()
	added, err := backend.PreventionAdd(ctx, contract.PreventionAddRequest{
		SchemaVersion: "v1", IssueID: issueID, Kind: "regression_test", Title: "regression",
		ArtifactPath: "artifact.txt", ContentSHA256: hash, Source: "test", Actor: "owner",
	})
	if err != nil || !added.Created {
		t.Fatalf("prevention add = %+v, %v", added, err)
	}
	if err := added.Validate(); err != nil {
		t.Fatalf("add response violates contract: %v", err)
	}
	if _, err := backend.PreventionActivate(ctx, contract.PreventionActivateRequest{
		SchemaVersion: "v1", ArtifactVersionID: added.Version.ArtifactVersionID, RepoPath: repo,
	}); !preventionAPIErrorCode(err, CodeConflict) {
		t.Fatalf("activate draft error = %v, want conflict", err)
	}
	failed, err := backend.PreventionVerify(ctx, contract.PreventionVerifyRequest{
		SchemaVersion: "v1", ArtifactVersionID: added.Version.ArtifactVersionID, RepoPath: repo,
		Command: []string{"errkb-command-that-cannot-start"},
	})
	if err != nil || failed.Verification.Passed || failed.Verification.ExitCode != nil {
		t.Fatalf("start failure response = %+v, %v", failed, err)
	}
	oldMode, hadMode := os.LookupEnv("ERRKB_PREVENTION_HELPER")
	if err := os.Setenv("ERRKB_PREVENTION_HELPER", "pass"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if hadMode {
			_ = os.Setenv("ERRKB_PREVENTION_HELPER", oldMode)
		} else {
			_ = os.Unsetenv("ERRKB_PREVENTION_HELPER")
		}
	}()
	verified, err := backend.PreventionVerify(ctx, contract.PreventionVerifyRequest{
		SchemaVersion: "v1", ArtifactVersionID: added.Version.ArtifactVersionID, RepoPath: repo,
		Command: preventionHelperCommand("pass"),
	})
	if err != nil || !verified.Verification.Passed || verified.Version.Status != "verified" {
		t.Fatalf("prevention verify = %+v, %v", verified, err)
	}
	activated, err := backend.PreventionActivate(ctx, contract.PreventionActivateRequest{
		SchemaVersion: "v1", ArtifactVersionID: added.Version.ArtifactVersionID, RepoPath: repo,
	})
	if err != nil || !activated.Activated || activated.Version.Status != "active" {
		t.Fatalf("prevention activate = %+v, %v", activated, err)
	}
	replayed, err := backend.PreventionActivate(ctx, contract.PreventionActivateRequest{
		SchemaVersion: "v1", ArtifactVersionID: added.Version.ArtifactVersionID, RepoPath: repo,
	})
	if err != nil || replayed.Activated {
		t.Fatalf("activation replay = %+v, %v", replayed, err)
	}
	listed, err := backend.PreventionList(ctx, contract.PreventionListRequest{SchemaVersion: "v1", IssueID: issueID, Status: "active"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].Versions == nil {
		t.Fatalf("prevention list = %+v, %v", listed, err)
	}
	detail, err := backend.PreventionGet(ctx, contract.PreventionGetRequest{SchemaVersion: "v1", ArtifactID: added.Artifact.ArtifactID})
	if err != nil || len(detail.Versions) != 1 || len(detail.Verifications) != 2 {
		t.Fatalf("prevention get = %+v, %v", detail, err)
	}
	for _, response := range []any{added, failed, verified, activated, listed, detail} {
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		text := string(encoded)
		if strings.Contains(text, repo) || strings.Contains(text, "original") || strings.Contains(text, "PATH=") {
			t.Fatalf("response leaked local data: %s", text)
		}
	}
	if _, err := backend.PreventionGet(ctx, contract.PreventionGetRequest{SchemaVersion: "v1", ArtifactID: "11111111-1111-4111-8111-111111111111"}); !preventionAPIErrorCode(err, CodeNotFound) {
		t.Fatalf("missing prevention get = %v, want not_found", err)
	}
}

func preventionAPIErrorCode(err error, code string) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.Code == code
}

func TestSQLBackendPreventionValidatesBeforeDatabase(t *testing.T) {
	backend := NewSQLBackend(nil)
	_, err := backend.PreventionAdd(context.Background(), contract.PreventionAddRequest{SchemaVersion: "v2"})
	if !preventionAPIErrorCode(err, CodeInvalidRequest) {
		t.Fatalf("invalid add error = %v", err)
	}
	_, err = backend.PreventionList(context.Background(), contract.PreventionListRequest{SchemaVersion: "v1", Cursor: "not-a-cursor"})
	if !preventionAPIErrorCode(err, CodeUnavailable) && !preventionAPIErrorCode(err, CodeInvalidRequest) {
		t.Fatalf("invalid cursor error = %v", fmt.Errorf("%w", err))
	}
}
