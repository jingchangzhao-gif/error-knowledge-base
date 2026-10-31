package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/mcp"
)

const (
	preventionIssueID    = "11111111-1111-4111-8111-111111111111"
	preventionArtifactID = "22222222-2222-4222-8222-222222222222"
	preventionVersionID  = "33333333-3333-4333-8333-333333333333"
)

func preventionArtifact() contract.PreventionArtifact {
	return contract.PreventionArtifact{
		ArtifactID: preventionArtifactID,
		ProjectKey: "demo",
		Kind:       "regression_test",
		Title:      "rename keeps the lock",
		IssueIDs:   []string{preventionIssueID},
		CreatedAt:  "2026-10-30T10:00:00Z",
		UpdatedAt:  "2026-10-30T10:00:00Z",
	}
}

func preventionVersion(status string) contract.PreventionVersion {
	version := contract.PreventionVersion{
		ArtifactVersionID: preventionVersionID,
		ArtifactID:        preventionArtifactID,
		Version:           1,
		ArtifactPath:      "tests/rename_lock_test.go",
		ContentSHA256:     strings.Repeat("a", 64),
		Status:            status,
		CreatedAt:         "2026-10-30T10:00:00Z",
	}
	if status == "active" {
		commit := strings.Repeat("b", 40)
		activatedAt := "2026-10-30T11:00:00Z"
		version.CommitSHA = &commit
		version.ActivatedAt = &activatedAt
	}
	return version
}

func validPreventionAddResponse() contract.PreventionAddResponse {
	return contract.PreventionAddResponse{
		SchemaVersion: "v1",
		Artifact:      preventionArtifact(),
		Version:       preventionVersion("draft"),
		Created:       true,
	}
}

func validPreventionListResponse() contract.PreventionListResponse {
	return contract.PreventionListResponse{
		SchemaVersion: "v1",
		Items: []contract.PreventionArtifactView{{
			Artifact: preventionArtifact(),
			Versions: []contract.PreventionVersion{preventionVersion("draft")},
		}},
	}
}

func validPreventionGetResponse() contract.PreventionGetResponse {
	return contract.PreventionGetResponse{
		SchemaVersion: "v1",
		Artifact:      preventionArtifact(),
		Versions:      []contract.PreventionVersion{preventionVersion("draft")},
		Verifications: []contract.PreventionVerification{},
	}
}

func (b *fakeBackend) PreventionAdd(_ context.Context, request contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.preventionAddRequests = append(b.preventionAddRequests, request)
	if b.preventionErr != nil {
		return contract.PreventionAddResponse{}, b.preventionErr
	}
	if b.preventionAddResponse != nil {
		return *b.preventionAddResponse, nil
	}
	return validPreventionAddResponse(), nil
}

func (b *fakeBackend) PreventionList(_ context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.preventionListRequests = append(b.preventionListRequests, request)
	if b.preventionErr != nil {
		return contract.PreventionListResponse{}, b.preventionErr
	}
	if b.preventionListResponse != nil {
		return *b.preventionListResponse, nil
	}
	return validPreventionListResponse(), nil
}

func (b *fakeBackend) PreventionGet(_ context.Context, request contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.preventionGetRequests = append(b.preventionGetRequests, request)
	if b.preventionErr != nil {
		return contract.PreventionGetResponse{}, b.preventionErr
	}
	if b.preventionGetResponse != nil {
		return *b.preventionGetResponse, nil
	}
	return validPreventionGetResponse(), nil
}

func preventionTool(t *testing.T, backend mcp.Backend, name string) mcp.Tool {
	t.Helper()
	for _, tool := range mcp.NewTools(backend) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not published", name)
	return mcp.Tool{}
}

func callPreventionTool(t *testing.T, tool mcp.Tool, arguments map[string]any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Call(context.Background(), raw)
}

func validRegisterArguments() map[string]any {
	return map[string]any{
		"issue_id":       preventionIssueID,
		"kind":           "regression_test",
		"title":          "rename keeps the lock",
		"artifact_path":  "tests/rename_lock_test.go",
		"content_sha256": strings.Repeat("a", 64),
	}
}

func TestPreventionRegisterDefaultsVersionAndFixesMCPIdentity(t *testing.T) {
	backend := newFakeBackend()
	tool := preventionTool(t, backend, mcp.ToolPreventionRegister)
	if tool.ReadOnly {
		t.Fatal("register must not be marked read-only")
	}
	result, err := callPreventionTool(t, tool, validRegisterArguments())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := result.(contract.PreventionAddResponse); !ok {
		t.Fatalf("register result has type %T", result)
	}
	if len(backend.preventionAddRequests) != 1 {
		t.Fatalf("backend calls = %d, want 1", len(backend.preventionAddRequests))
	}
	request := backend.preventionAddRequests[0]
	if request.SchemaVersion != "v1" || request.Source != "mcp" || request.Actor != "mcp" {
		t.Fatalf("register request = %+v, want v1 and fixed mcp identity", request)
	}
}

func TestPreventionListAndGetValidateAndForwardRequests(t *testing.T) {
	backend := newFakeBackend()
	limit := 7
	listResult, err := callPreventionTool(t, preventionTool(t, backend, mcp.ToolPreventionList), map[string]any{
		"issue_id": preventionIssueID, "project_key": "demo", "kind": "regression_test",
		"status": "draft", "limit": limit, "cursor": "cursor-1",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, ok := listResult.(contract.PreventionListResponse); !ok {
		t.Fatalf("list result has type %T", listResult)
	}
	if len(backend.preventionListRequests) != 1 {
		t.Fatalf("list backend calls = %d", len(backend.preventionListRequests))
	}
	request := backend.preventionListRequests[0]
	if request.SchemaVersion != "v1" || request.IssueID != preventionIssueID || request.ProjectKey != "demo" ||
		request.Kind != "regression_test" || request.Status != "draft" || request.Limit == nil || *request.Limit != limit || request.Cursor != "cursor-1" {
		t.Fatalf("list request = %+v", request)
	}

	getResult, err := callPreventionTool(t, preventionTool(t, backend, mcp.ToolPreventionGet), map[string]any{"artifact_id": preventionArtifactID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, ok := getResult.(contract.PreventionGetResponse); !ok {
		t.Fatalf("get result has type %T", getResult)
	}
	if len(backend.preventionGetRequests) != 1 || backend.preventionGetRequests[0].SchemaVersion != "v1" || backend.preventionGetRequests[0].ArtifactID != preventionArtifactID {
		t.Fatalf("get requests = %+v", backend.preventionGetRequests)
	}
	for _, name := range []string{mcp.ToolPreventionList, mcp.ToolPreventionGet} {
		if !preventionTool(t, backend, name).ReadOnly {
			t.Fatalf("%s must be read-only", name)
		}
	}
}

func TestPreventionToolsRejectInvalidAndUnknownArguments(t *testing.T) {
	backend := newFakeBackend()
	register := preventionTool(t, backend, mcp.ToolPreventionRegister)
	cases := []struct {
		name      string
		tool      mcp.Tool
		arguments map[string]any
	}{
		{name: "explicit register version", tool: register, arguments: map[string]any{"schema_version": "v2"}},
		{name: "explicit list version", tool: preventionTool(t, backend, mcp.ToolPreventionList), arguments: map[string]any{"schema_version": "v2"}},
		{name: "explicit get version", tool: preventionTool(t, backend, mcp.ToolPreventionGet), arguments: map[string]any{"artifact_id": preventionArtifactID, "schema_version": "v2"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := callPreventionTool(t, testCase.tool, testCase.arguments); !errors.Is(err, mcp.ErrInvalidArguments) {
				t.Fatalf("error = %v, want invalid arguments", err)
			}
		})
	}

	invalidRegisters := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "kind", mutate: func(arguments map[string]any) { arguments["kind"] = "shell_command" }},
		{name: "hash", mutate: func(arguments map[string]any) { arguments["content_sha256"] = "ABC" }},
		{name: "absolute path", mutate: func(arguments map[string]any) { arguments["artifact_path"] = `C:\\repo\\test.go` }},
		{name: "parent path", mutate: func(arguments map[string]any) { arguments["artifact_path"] = "../test.go" }},
		{name: "status", mutate: func(arguments map[string]any) { arguments["status"] = "active" }},
		{name: "repo path", mutate: func(arguments map[string]any) { arguments["repo_path"] = "C:/repo" }},
		{name: "command", mutate: func(arguments map[string]any) { arguments["command"] = []string{"go", "test"} }},
		{name: "body", mutate: func(arguments map[string]any) { arguments["body"] = "package secret" }},
		{name: "source", mutate: func(arguments map[string]any) { arguments["source"] = "attacker" }},
		{name: "actor", mutate: func(arguments map[string]any) { arguments["actor"] = "attacker" }},
		{name: "unknown", mutate: func(arguments map[string]any) { arguments["surprise"] = true }},
	}
	for _, testCase := range invalidRegisters {
		t.Run(testCase.name, func(t *testing.T) {
			arguments := validRegisterArguments()
			testCase.mutate(arguments)
			if _, err := callPreventionTool(t, register, arguments); !errors.Is(err, mcp.ErrInvalidArguments) {
				t.Fatalf("error = %v, want invalid arguments", err)
			}
		})
	}
	if len(backend.preventionAddRequests) != 0 || len(backend.preventionListRequests) != 0 || len(backend.preventionGetRequests) != 0 {
		t.Fatal("invalid prevention arguments reached the backend")
	}
}

func TestPreventionToolsValidateBackendResponses(t *testing.T) {
	backend := newFakeBackend()
	invalid := validPreventionAddResponse()
	invalid.Version.Status = "invented"
	backend.preventionAddResponse = &invalid
	_, err := callPreventionTool(t, preventionTool(t, backend, mcp.ToolPreventionRegister), validRegisterArguments())
	if err == nil || errors.Is(err, mcp.ErrInvalidArguments) || !strings.Contains(err.Error(), "breaks the tool contract") {
		t.Fatalf("response drift error = %v", err)
	}
}

func TestPreventionBackendFailuresKeepJSONRPCMapping(t *testing.T) {
	backend := newFakeBackend()
	h := startHarness(t, backend)
	calls := []struct {
		name string
		args map[string]any
	}{
		{name: mcp.ToolPreventionRegister, args: validRegisterArguments()},
		{name: mcp.ToolPreventionGet, args: map[string]any{"artifact_id": preventionArtifactID}},
	}
	for index, call := range calls {
		backend.preventionErr = errors.New(map[int]string{0: "conflict: content changed", 1: "not_found: no artifact"}[index])
		h.send(index+1, "tools/call", map[string]any{"name": call.name, "arguments": call.args})
		if code := errorCodeOf(t, h.receive()); code != -32603 {
			t.Fatalf("%s backend error code = %v, want existing internal-error mapping", call.name, code)
		}
	}
}

func TestMCPBackendHasNoPreventionVerificationOrActivation(t *testing.T) {
	backendType := reflect.TypeOf((*mcp.Backend)(nil)).Elem()
	for _, forbidden := range []string{"PreventionVerify", "PreventionActivate"} {
		if _, exists := backendType.MethodByName(forbidden); exists {
			t.Fatalf("MCP Backend exposes forbidden %s", forbidden)
		}
	}
	for _, tool := range mcp.NewTools(newFakeBackend()) {
		if strings.Contains(tool.Name, "prevention_verify") || strings.Contains(tool.Name, "prevention_activate") {
			t.Fatalf("MCP publishes executable prevention transition %q", tool.Name)
		}
	}
}
