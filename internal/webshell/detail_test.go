package webshell

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

func detailServer(t *testing.T, detail GetIssueFunc) *Server {
	t.Helper()
	server, err := NewWithDetail(func(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
		return contract.SearchResponse{SchemaVersion: "v1"}, nil
	}, detail)
	if err != nil {
		t.Fatalf("NewWithDetail: %v", err)
	}
	return server
}

func TestDetailRequiresTheToken(t *testing.T) {
	server := detailServer(t, func(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
		return contract.GetIssueResponse{}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/issues/abc", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("without a token: got %d, want 401", response.Code)
	}
}

func TestDetailReturnsTheStoredIssue(t *testing.T) {
	const issue = "11111111-1111-4111-8111-111111111111"
	var seen string
	server := detailServer(t, func(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
		seen = request.IssueID
		return contract.GetIssueResponse{
			SchemaVersion: "v1",
			Issue: contract.IssueDetailV{
				ID:          issue,
				Fingerprint: "4202fb44db19",
				Title:       "pnpm build failed",
				Status:      "resolved",
				Hits:        3,
				Trust:       contract.TrustView{Score: 0.6, Samples: 5},
				Solutions: []contract.SolutionV{{
					ID:     "solution-1",
					Status: "verified",
					Versions: []contract.VersionV{{
						ID:      "version-1",
						Version: 1,
						Body:    "install the workspace deps first",
						Source:  "plugin",
					}},
				}},
			},
		}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/api/issues/11111111-1111-4111-8111-111111111111", nil)
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", response.Code, response.Body.String())
	}
	if seen != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("the daemon saw issue %q", seen)
	}
	body := response.Body.String()
	for _, want := range []string{"pnpm build failed", "4202fb44db19", "install the workspace deps first", `"score":0.6`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body does not contain %q: %s", want, body)
		}
	}
}

func TestDetailReportsAMissingIssue(t *testing.T) {
	server := detailServer(t, func(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
		return contract.GetIssueResponse{}, errors.New("not_found: no issue has that id")
	})
	request := httptest.NewRequest(http.MethodGet, "/api/issues/22222222-2222-4222-8222-222222222222", nil)
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", response.Code, response.Body.String())
	}
}

func TestDetailNeedsAReader(t *testing.T) {
	server, err := New(func(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
		return contract.SearchResponse{SchemaVersion: "v1"}, nil
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/issues/abc", nil)
	request.Header.Set("x-errkb-token", server.Token())
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", response.Code)
	}
}

func TestDetailPageServesTheShell(t *testing.T) {
	server := detailServer(t, nil)
	request := httptest.NewRequest(http.MethodGet, "/issues/abc", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), server.Token()) {
		t.Fatal("the page did not carry the per-launch token")
	}
}

func TestDetailAssetsIncludeReadOnlyPreventions(t *testing.T) {
	server := detailServer(t, nil)
	script, err := fs.ReadFile(server.assets, "app.js")
	if err != nil {
		t.Fatalf("read the detail script: %v", err)
	}
	text := string(script)
	for _, wanted := range []string{
		"function renderPreventions", "Promise.allSettled", "/preventions",
		"errkb prevention verify", "errkb prevention activate", "latest verification",
		"activated_at", "commit_sha", "Historical version",
	} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("detail script does not contain %q", wanted)
		}
	}
	for _, forbidden := range []string{"data-prevention-action", "prevention/verify", "prevention/activate", "prevented "} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("detail script exposes forbidden prevention behavior %q", forbidden)
		}
	}
}
