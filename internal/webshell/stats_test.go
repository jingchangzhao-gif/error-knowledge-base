package webshell

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/audit"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

func doJSON(t *testing.T, method, url, token string, body io.Reader) ([]byte, int) {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("X-Errkb-Token", token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	return payload, response.StatusCode
}

func TestStatisticsPayloadIsTheCollectedSnapshot(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	generated := time.Date(2026, 10, 22, 1, 2, 3, 0, time.UTC)
	server.EnableStatistics(func(context.Context) (audit.Stats, error) {
		return audit.Stats{
			GeneratedAt: generated,
			Retrieval:   audit.RetrievalStats{Rounds: 3, ExactTopOne: 2},
			Latency:     audit.LatencyStats{Samples: 4, P50Milliseconds: 12.5},
			Savings:     audit.SavingsStats{AssumedTokensPerDiagnosis: 400, Assumption: "one diagnosis"},
		}, nil
	}, nil)
	payload, code := doJSON(t, http.MethodGet, httpServer.URL+"/api/stats", server.Token(), nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, payload)
	}
	var decoded struct {
		Stats audit.Stats `json:"stats"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Stats.GeneratedAt.Equal(generated) || decoded.Stats.Retrieval.Rounds != 3 ||
		decoded.Stats.Latency.P50Milliseconds != 12.5 || decoded.Stats.Savings.Assumption != "one diagnosis" {
		t.Fatalf("stats = %+v", decoded.Stats)
	}
}

func TestStatisticsAndAuditRequireTheToken(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	server.EnableStatistics(func(context.Context) (audit.Stats, error) { return audit.Stats{}, nil },
		func(context.Context, audit.Filter) (audit.Page, error) { return audit.Page{}, nil })
	for _, path := range []string{"/api/stats", "/api/audit"} {
		if _, code := doJSON(t, http.MethodGet, httpServer.URL+path, "", nil); code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", path, code)
		}
	}
}

func TestAuditFiltersAreParsedAndForwarded(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	var got audit.Filter
	server.EnableStatistics(nil, func(_ context.Context, filter audit.Filter) (audit.Page, error) {
		got = filter
		return audit.Page{Total: 2, Limit: 10, Records: []audit.Row{{ID: 7, Action: "delete.scope"}}}, nil
	})
	url := httpServer.URL + "/api/audit?action=delete.scope&entity=issue&entity_id=abc&limit=10&offset=5&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z"
	payload, code := doJSON(t, http.MethodGet, url, server.Token(), nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", code, payload)
	}
	if got.Action != "delete.scope" || got.Entity != "issue" || got.EntityID != "abc" || got.Limit != 10 || got.Offset != 5 {
		t.Fatalf("filter = %+v", got)
	}
	if got.From.IsZero() || got.To.IsZero() {
		t.Fatalf("times were not parsed: %+v", got)
	}
	var page audit.Page
	if err := json.Unmarshal(payload, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if page.Total != 2 || len(page.Records) != 1 {
		t.Fatalf("page = %+v", page)
	}
}

func TestAuditRejectsMalformedFilters(t *testing.T) {
	server, httpServer := newTestServer(t)
	defer httpServer.Close()
	server.EnableStatistics(nil, func(context.Context, audit.Filter) (audit.Page, error) { return audit.Page{}, nil })
	for _, query := range []string{"limit=-1", "offset=x", "from=yesterday"} {
		if _, code := doJSON(t, http.MethodGet, httpServer.URL+"/api/audit?"+query, server.Token(), nil); code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", query, code)
		}
	}
}

func TestDeletionPreviewIncludesPreventionCounts(t *testing.T) {
	store := &fakeSettingsStore{config: settingsConfig()}
	data := &fakeSettingsData{preview: database.DeleteCounts{
		PreventionArtifacts: 1, PreventionArtifactVersions: 2,
		IssuePreventions: 3, PreventionVerifications: 4,
	}}
	server, base := newSettingsShell(t, store, data)
	payload, code := doJSON(t, http.MethodPost, base+"/api/deletions/preview", server.Token(), strings.NewReader(`{"project_key":"p1"}`))
	if code != http.StatusOK {
		t.Fatalf("preview: status = %d, body = %s", code, payload)
	}
	var response struct {
		Counts map[string]int64 `json:"counts"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	for name, want := range map[string]int64{
		"prevention_artifacts": 1, "prevention_artifact_versions": 2,
		"issue_preventions": 3, "prevention_verifications": 4,
	} {
		if response.Counts[name] != want {
			t.Errorf("%s = %d, want %d; payload = %s", name, response.Counts[name], want, payload)
		}
	}
}
