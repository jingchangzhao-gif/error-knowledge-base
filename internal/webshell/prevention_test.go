package webshell

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

const preventionWebIssueID = "11111111-1111-4111-8111-111111111111"

func preventionWebResponse(items []contract.PreventionArtifactView) contract.PreventionListResponse {
	return contract.PreventionListResponse{SchemaVersion: "v1", Items: items}
}

func preventionWebArtifact() contract.PreventionArtifactView {
	return contract.PreventionArtifactView{
		Artifact: contract.PreventionArtifact{
			ArtifactID: "22222222-2222-4222-8222-222222222222",
			ProjectKey: "demo",
			Kind:       "regression_test",
			Title:      "rename keeps the lock",
			IssueIDs:   []string{preventionWebIssueID},
			CreatedAt:  "2026-10-30T10:00:00Z",
			UpdatedAt:  "2026-10-30T10:00:00Z",
		},
		Versions: []contract.PreventionVersion{{
			ArtifactVersionID: "33333333-3333-4333-8333-333333333333",
			ArtifactID:        "22222222-2222-4222-8222-222222222222",
			Version:           1,
			ArtifactPath:      "tests/rename_lock_test.go",
			ContentSHA256:     strings.Repeat("a", 64),
			Status:            "draft",
			CreatedAt:         "2026-10-30T10:00:00Z",
		}},
	}
}

func preventionRequest(t *testing.T, server *Server, method, path string, authorized bool) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if authorized {
		request.Header.Set("x-errkb-token", server.Token())
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestPreventionListReturnsArtifactsAndVersions(t *testing.T) {
	var seen contract.PreventionListRequest
	server := detailServer(t, nil)
	server.EnablePreventions(func(_ context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
		seen = request
		return preventionWebResponse([]contract.PreventionArtifactView{preventionWebArtifact()}), nil
	})
	response := preventionRequest(t, server, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", true)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if seen.SchemaVersion != "v1" || seen.IssueID != preventionWebIssueID || seen.ProjectKey != "" || seen.Kind != "" || seen.Status != "" || seen.Limit != nil || seen.Cursor != "" {
		t.Fatalf("backend request = %+v", seen)
	}
	var payload contract.PreventionListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if err := payload.Validate(); err != nil {
		t.Fatalf("invalid contract response: %v", err)
	}
	if len(payload.Items) != 1 || len(payload.Items[0].Versions) != 1 {
		t.Fatalf("response = %+v", payload)
	}
}

func TestPreventionListReturnsAnEmptyArray(t *testing.T) {
	server := detailServer(t, nil)
	server.EnablePreventions(func(_ context.Context, _ contract.PreventionListRequest) (contract.PreventionListResponse, error) {
		return preventionWebResponse([]contract.PreventionArtifactView{}), nil
	})
	response := preventionRequest(t, server, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPreventionListAuthorizationAndUnavailableStates(t *testing.T) {
	calls := 0
	server := detailServer(t, nil)
	server.EnablePreventions(func(_ context.Context, _ contract.PreventionListRequest) (contract.PreventionListResponse, error) {
		calls++
		return preventionWebResponse([]contract.PreventionArtifactView{}), nil
	})
	if response := preventionRequest(t, server, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", false); response.Code != http.StatusUnauthorized {
		t.Fatalf("without token: status = %d, want 401", response.Code)
	}
	if response := preventionRequest(t, server, http.MethodGet, "/api/issues/not-a-uuid/preventions", true); response.Code != http.StatusNotFound {
		t.Fatalf("invalid UUID: status = %d, want 404", response.Code)
	}
	if calls != 0 {
		t.Fatalf("authorization/UUID failures reached backend %d times", calls)
	}

	unconfigured := detailServer(t, nil)
	if response := preventionRequest(t, unconfigured, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("without prevention reader: status = %d, want 503", response.Code)
	}

	server.EnablePreventions(func(_ context.Context, _ contract.PreventionListRequest) (contract.PreventionListResponse, error) {
		return contract.PreventionListResponse{}, errors.New("daemon unavailable")
	})
	if response := preventionRequest(t, server, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", true); response.Code != http.StatusBadGateway {
		t.Fatalf("backend failure: status = %d, want 502", response.Code)
	}
}

func TestPreventionRoutesAreReadOnlyAndKeepSecurityHeaders(t *testing.T) {
	calls := 0
	server := detailServer(t, nil)
	server.EnablePreventions(func(_ context.Context, _ contract.PreventionListRequest) (contract.PreventionListResponse, error) {
		calls++
		return preventionWebResponse([]contract.PreventionArtifactView{}), nil
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := preventionRequest(t, server, method, "/api/issues/"+preventionWebIssueID+"/preventions", true)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("write methods reached prevention reader %d times", calls)
	}
	response := preventionRequest(t, server, http.MethodGet, "/api/issues/"+preventionWebIssueID+"/preventions", true)
	csp := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "form-action 'none'") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP changed: %q", csp)
	}
}

func TestPreventionStatsEndpointIsScopedAndReadOnly(t *testing.T) {
	var seen contract.PreventionStatsRequest
	server := detailServer(t, nil)
	server.EnablePreventionStats(func(_ context.Context, request contract.PreventionStatsRequest) (contract.PreventionStatsResponse, error) {
		seen = request
		return contract.PreventionStatsResponse{
			SchemaVersion: "v1", ByStatus: []contract.PreventionCount{}, ByKind: []contract.PreventionCount{},
			Activations: []contract.PreventionActivationStats{{
				IssueID: preventionWebIssueID, ArtifactID: "artifact-1", ArtifactVersionID: "version-1",
				ActivatedAt: "2026-10-30T00:01:00Z", CommitSHA: strings.Repeat("1", 40), Status: "active",
				ObservationStatus: "observing",
			}},
			MatureProtectionRate: nil, RecurringVersionIDs: []string{},
		}, nil
	})
	response := preventionRequest(t, server, http.MethodGet, "/api/preventions/stats?project=demo&issue="+preventionWebIssueID, true)
	if response.Code != http.StatusOK || seen.ProjectKey != "demo" || seen.IssueID != preventionWebIssueID {
		t.Fatalf("status = %d, request = %+v, body = %s", response.Code, seen, response.Body.String())
	}
	var payload contract.PreventionStatsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Validate() != nil || payload.MatureProtectionRate != nil {
		t.Fatalf("stats payload = %+v, %v", payload, err)
	}
	if strings.Contains(response.Body.String(), "prevented_count") {
		t.Fatalf("response invented prevented count: %s", response.Body.String())
	}
	if response := preventionRequest(t, server, http.MethodGet, "/api/preventions/stats", false); response.Code != http.StatusUnauthorized {
		t.Fatalf("without token: status = %d, want 401", response.Code)
	}
	if response := preventionRequest(t, server, http.MethodPost, "/api/preventions/stats", true); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", response.Code)
	}
	unconfigured := detailServer(t, nil)
	if response := preventionRequest(t, unconfigured, http.MethodGet, "/api/preventions/stats", true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("without stats reader: status = %d, want 503", response.Code)
	}
}

func TestPreventionStatsAssetsExplainObservationLimits(t *testing.T) {
	source, err := os.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{"renderPreventionStats", "/api/preventions/stats", "暂无成熟记录", "观察中", "recurrence_count", "before_days", "after_days"} {
		if !strings.Contains(text, required) {
			t.Fatalf("statistics assets lack %q", required)
		}
	}
	if strings.Contains(text, "prevented"+"_count") {
		t.Fatal("statistics assets must not invent prevented error counts")
	}
}
