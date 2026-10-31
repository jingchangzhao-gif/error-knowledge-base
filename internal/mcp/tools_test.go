package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/mcp"
)

const testVersion = "0.1.0-test"

// fakeBackend records what reached the core and answers with contract-valid
// responses. It never touches a database.
type fakeBackend struct {
	mu sync.Mutex

	lookups  []contract.LookupRequest
	searches []contract.SearchRequest
	records  []contract.RecordRequest
	feedback []contract.FeedbackRequest
	lists    []contract.ListRequest
	stats    []contract.StatsRequest

	solutionRequests []contract.RecordSolutionRequest
	statusRequests   []contract.SetStatusRequest
	issueRequests    []contract.GetIssueRequest

	preventionAddRequests  []contract.PreventionAddRequest
	preventionListRequests []contract.PreventionListRequest
	preventionGetRequests  []contract.PreventionGetRequest
	preventionAddResponse  *contract.PreventionAddResponse
	preventionListResponse *contract.PreventionListResponse
	preventionGetResponse  *contract.PreventionGetResponse
	preventionErr          error

	searchDelays []time.Duration
	searchCalls  int
	searchErr    error
}

func newFakeBackend() *fakeBackend { return &fakeBackend{} }

func (b *fakeBackend) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	b.mu.Lock()
	b.lookups = append(b.lookups, request)
	b.mu.Unlock()
	return contract.LookupResponse{
		SchemaVersion: request.SchemaVersion,
		Candidates:    []contract.Candidate{fakeCandidate()},
	}, nil
}

func (b *fakeBackend) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	b.mu.Lock()
	b.searches = append(b.searches, request)
	index := b.searchCalls
	b.searchCalls++
	failure := b.searchErr
	var delay time.Duration
	if index < len(b.searchDelays) {
		delay = b.searchDelays[index]
	}
	b.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return contract.SearchResponse{}, ctx.Err()
		}
	}
	if failure != nil {
		return contract.SearchResponse{}, failure
	}
	return contract.SearchResponse{
		SchemaVersion: request.SchemaVersion,
		Candidates:    []contract.Candidate{fakeCandidate()},
	}, nil
}

func (b *fakeBackend) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	b.mu.Lock()
	b.records = append(b.records, request)
	b.mu.Unlock()
	return contract.RecordResponse{
		SchemaVersion: request.SchemaVersion,
		EventID:       request.Event.EventID,
		OccurrenceID:  "00000000-0000-4000-8000-000000000002",
		IssueID:       "00000000-0000-4000-8000-000000000003",
		Fingerprint:   request.Event.Fingerprint,
		Created:       true,
	}, nil
}

func (b *fakeBackend) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	b.mu.Lock()
	b.feedback = append(b.feedback, request)
	b.mu.Unlock()
	return contract.FeedbackResponse{
		SchemaVersion: request.SchemaVersion,
		OutcomeID:     "00000000-0000-4000-8000-000000000006",
		Recorded:      true,
	}, nil
}

func (b *fakeBackend) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	b.mu.Lock()
	b.lists = append(b.lists, request)
	b.mu.Unlock()
	return contract.ListResponse{
		SchemaVersion: request.SchemaVersion,
		Items: []contract.ListItem{{
			ID:        "00000000-0000-4000-8000-000000000004",
			Title:     "EPERM on rename",
			UpdatedAt: "2026-10-19T10:00:00Z",
		}},
	}, nil
}

func (b *fakeBackend) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	b.mu.Lock()
	b.stats = append(b.stats, request)
	b.mu.Unlock()
	return contract.StatsResponse{
		SchemaVersion: request.SchemaVersion,
		Totals:        contract.StatsTotals{Issues: 2, Events: 3},
	}, nil
}

func (b *fakeBackend) RecordSolution(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	b.mu.Lock()
	b.solutionRequests = append(b.solutionRequests, request)
	b.mu.Unlock()
	return contract.RecordSolutionResponse{
		SchemaVersion:     request.SchemaVersion,
		SolutionID:        "00000000-0000-4000-8000-00000000000a",
		SolutionVersionID: "00000000-0000-4000-8000-00000000000b",
		Version:           1,
		Recorded:          true,
	}, nil
}

func (b *fakeBackend) SetStatus(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	b.mu.Lock()
	b.statusRequests = append(b.statusRequests, request)
	b.mu.Unlock()
	return contract.SetStatusResponse{
		SchemaVersion: request.SchemaVersion,
		IssueID:       request.IssueID,
		Status:        request.Status,
	}, nil
}

func (b *fakeBackend) GetIssue(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	b.mu.Lock()
	b.issueRequests = append(b.issueRequests, request)
	b.mu.Unlock()
	return contract.GetIssueResponse{
		SchemaVersion: request.SchemaVersion,
		Issue: contract.IssueDetailV{
			ID:          request.IssueID,
			Fingerprint: "000000000000",
			Title:       "fake issue",
		},
	}, nil
}

func (b *fakeBackend) lookupRequests() []contract.LookupRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.LookupRequest(nil), b.lookups...)
}

func (b *fakeBackend) searchRequests() []contract.SearchRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.SearchRequest(nil), b.searches...)
}

func (b *fakeBackend) recordRequests() []contract.RecordRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.RecordRequest(nil), b.records...)
}

func (b *fakeBackend) feedbackRequests() []contract.FeedbackRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]contract.FeedbackRequest(nil), b.feedback...)
}

func (b *fakeBackend) setSearchDelays(delays ...time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.searchDelays = delays
	b.searchCalls = 0
}

func fakeCandidate() contract.Candidate {
	confidence := 0.8
	return contract.Candidate{
		IssueID:     "00000000-0000-4000-8000-000000000004",
		Fingerprint: "0123456789ab",
		Title:       "EPERM on rename",
		MatchReason: "jaccard_rerank",
		MatchDetail: "score 0.80",
		Approximate: true,
		Confidence:  &confidence,
	}
}

// harness runs one server over in-memory pipes so stdout, stderr and the input
// stream are observable in the test.
type harness struct {
	t       *testing.T
	backend *fakeBackend
	in      *io.PipeWriter
	out     *bufio.Reader
	outPipe *io.PipeWriter
	logs    *safeBuffer
	done    chan error
	once    sync.Once
	stopped atomic.Bool
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startHarness(t *testing.T, backend *fakeBackend) *harness {
	t.Helper()
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	logs := &safeBuffer{}
	server := mcp.NewServer(inReader, outWriter, logs, backend, testVersion)
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	h := &harness{t: t, backend: backend, in: inWriter, out: bufio.NewReader(outReader), outPipe: outWriter, logs: logs, done: done}
	t.Cleanup(func() {
		h.close()
	})
	return h
}

// close ends the session and waits for Serve to return. It is idempotent so a
// test that already stopped the server through EOF does not block twice.
func (h *harness) close() {
	_ = h.in.Close()
	_ = h.outPipe.Close()
	if h.stopped.Load() {
		return
	}
	h.once.Do(func() {
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			h.t.Errorf("the MCP server did not stop")
		}
	})
}

// stop closes the input stream and returns Serve's error.
func (h *harness) stop() error {
	_ = h.in.Close()
	select {
	case err := <-h.done:
		h.stopped.Store(true)
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatalf("the MCP server did not stop after EOF")
		return nil
	}
}

func (h *harness) send(id any, method string, params any) {
	h.t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		h.t.Fatalf("encode the request: %v", err)
	}
	h.sendLine(string(encoded))
}

func (h *harness) sendNotification(method string, params any) {
	h.t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		request["params"] = params
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		h.t.Fatalf("encode the notification: %v", err)
	}
	h.sendLine(string(encoded))
}

func (h *harness) sendLine(line string) {
	h.t.Helper()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		h.t.Fatalf("write a frame: %v", err)
	}
}

// receive reads one stdout line and insists it is a JSON-RPC frame.
func (h *harness) receive() map[string]any {
	h.t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil {
		h.t.Fatalf("read a response frame: %v (server log: %s)", err, h.logs.String())
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(line), &frame); err != nil {
		h.t.Fatalf("stdout carried a non-JSON line %q: %v", strings.TrimSpace(line), err)
	}
	if frame["jsonrpc"] != "2.0" {
		h.t.Fatalf("stdout frame lost its jsonrpc version: %v", frame)
	}
	return frame
}

func resultOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("frame carries no result: %v", frame)
	}
	return result
}

func errorCodeOf(t *testing.T, frame map[string]any) float64 {
	t.Helper()
	rpcError, ok := frame["error"].(map[string]any)
	if !ok {
		t.Fatalf("frame carries no error: %v", frame)
	}
	code, ok := rpcError["code"].(float64)
	if !ok {
		t.Fatalf("frame error carries no numeric code: %v", frame)
	}
	return code
}

// resultText pulls the text content out of a tools/call answer.
func resultText(t *testing.T, frame map[string]any) string {
	t.Helper()
	result := resultOf(t, frame)
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("frame carries no content: %v", frame)
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["type"] != "text" {
		t.Fatalf("content is not a text block: %v", frame)
	}
	text, ok := item["text"].(string)
	if !ok || text == "" {
		t.Fatalf("text block is empty: %v", frame)
	}
	return text
}

func eventPayload(eventID string) map[string]any {
	return map[string]any{
		"schema_version":     "v1",
		"event_id":           eventID,
		"connector_id":       "00000000-0000-4000-8000-000000000001",
		"session_id":         "00000000-0000-4000-8000-000000000009",
		"observed_at":        "2026-10-19T10:00:00Z",
		"channel":            "B",
		"category":           "command-exit",
		"severity":           "error",
		"transient":          false,
		"headline":           "eperm: operation not permitted, rename '<path>/pkg'",
		"normalized_message": "eperm: operation not permitted, rename '<path>/pkg'",
		"redacted_sample":    "eperm: operation not permitted, rename '<path>/pkg'",
		"os":                 "windows",
		"project_key":        "demo",
		"fingerprint":        "0123456789ab",
		"evidence":           "errkb run -- cmd",
		"confidence":         0.5,
	}
}

// TestInitializeStatesMCPIsNotPassiveCapture covers the plan §3.1 correction:
// MCP answers the hosts that ask; it is not a passive collector.
func TestInitializeStatesMCPIsNotPassiveCapture(t *testing.T) {
	h := startHarness(t, newFakeBackend())
	h.send(1, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "test client", "version": "1.0"},
	})
	frame := h.receive()
	result := resultOf(t, frame)

	if got := result["protocolVersion"]; got != mcp.ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %q", got, mcp.ProtocolVersion)
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo missing: %v", frame)
	}
	if serverInfo["name"] != mcp.ServerName || serverInfo["version"] != testVersion {
		t.Fatalf("serverInfo = %v, want %q/%q", serverInfo, mcp.ServerName, testVersion)
	}
	instructions, ok := result["instructions"].(string)
	if !ok {
		t.Fatalf("initialize carries no instructions: %v", frame)
	}
	lowered := strings.ToLower(instructions)
	for _, wanted := range []string{"passive", "mcp", "prevention", "draft", "verify", "activate", "cli", "only"} {
		if !strings.Contains(lowered, wanted) {
			t.Fatalf("instructions do not mention %q (got %q)", wanted, instructions)
		}
	}
}

// TestToolsListPublishesTheTwelveCoreTools also bans passive capture and
// executable prevention verification or activation.
func TestToolsListPublishesTheTwelveCoreTools(t *testing.T) {
	h := startHarness(t, newFakeBackend())
	h.send(1, "tools/list", nil)
	frame := h.receive()
	result := resultOf(t, frame)

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list carries no array: %v", frame)
	}
	wanted := []string{
		mcp.ToolLookup, mcp.ToolSearch, mcp.ToolRecord,
		mcp.ToolFeedback, mcp.ToolList, mcp.ToolStats,
		mcp.ToolRecordSolution, mcp.ToolSetStatus, mcp.ToolGetIssue,
		mcp.ToolPreventionRegister, mcp.ToolPreventionList, mcp.ToolPreventionGet,
	}
	if len(tools) != len(wanted) {
		t.Fatalf("tool count = %d, want %d (%v)", len(tools), len(wanted), tools)
	}
	seen := make(map[string]bool, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool descriptor is not an object: %v", raw)
		}
		name, _ := tool["name"].(string)
		seen[name] = true
		description, _ := tool["description"].(string)
		if description == "" {
			t.Fatalf("tool %q has no description", name)
		}
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			t.Fatalf("tool %q has no inputSchema", name)
		}
		lowered := strings.ToLower(name)
		for _, banned := range []string{"capture", "subscribe", "watch", "push", "passive"} {
			if strings.Contains(lowered, banned) {
				t.Fatalf("tool %q would be a passive-capture surface", name)
			}
		}
	}
	for _, name := range wanted {
		if !seen[name] {
			t.Fatalf("tool %q missing from tools/list", name)
		}
	}
}

// TestEveryToolReturnsContractJSON calls all nine tools and re-validates the
// payload against the contract.
func TestEveryToolReturnsContractJSON(t *testing.T) {
	backend := newFakeBackend()
	h := startHarness(t, backend)

	cases := []struct {
		tool      string
		arguments map[string]any
		validate  func([]byte) error
	}{
		{
			tool:      mcp.ToolLookup,
			arguments: map[string]any{"fingerprint": "0123456789ab"},
			validate: func(raw []byte) error {
				var response contract.LookupResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool:      mcp.ToolSearch,
			arguments: map[string]any{"query": "permission denied", "limit": 2},
			validate: func(raw []byte) error {
				var response contract.SearchResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool:      mcp.ToolRecord,
			arguments: map[string]any{"event": eventPayload("00000000-0000-4000-8000-000000000007")},
			validate: func(raw []byte) error {
				var response contract.RecordResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool: mcp.ToolFeedback,
			arguments: map[string]any{
				"issue_id":            "00000000-0000-4000-8000-000000000004",
				"solution_version_id": "00000000-0000-4000-8000-000000000005",
				"outcome":             "success",
				"note":                "worked on this host",
			},
			validate: func(raw []byte) error {
				var response contract.FeedbackResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool:      mcp.ToolList,
			arguments: map[string]any{"kind": "issue", "limit": 5},
			validate: func(raw []byte) error {
				var response contract.ListResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool:      mcp.ToolStats,
			arguments: map[string]any{"project_key": "demo"},
			validate: func(raw []byte) error {
				var response contract.StatsResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool: mcp.ToolRecordSolution,
			arguments: map[string]any{
				"issue_id":      "00000000-0000-4000-8000-000000000004",
				"body":          "chmod the directory, then retry",
				"preconditions": "linux only",
			},
			validate: func(raw []byte) error {
				var response contract.RecordSolutionResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool: mcp.ToolSetStatus,
			arguments: map[string]any{
				"issue_id": "00000000-0000-4000-8000-000000000004",
				"status":   "wontfix",
			},
			validate: func(raw []byte) error {
				var response contract.SetStatusResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			tool:      mcp.ToolGetIssue,
			arguments: map[string]any{"issue_id": "00000000-0000-4000-8000-000000000004"},
			validate: func(raw []byte) error {
				var response contract.GetIssueResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
	}

	for index, testCase := range cases {
		h.send(index+1, "tools/call", map[string]any{
			"name":      testCase.tool,
			"arguments": testCase.arguments,
		})
		frame := h.receive()
		text := resultText(t, frame)
		if err := testCase.validate([]byte(text)); err != nil {
			t.Fatalf("%s returned an invalid contract payload: %v\n%s", testCase.tool, err, text)
		}
	}

	if got := len(backend.lookupRequests()); got != 1 {
		t.Fatalf("lookup calls reached the backend %d times, want 1", got)
	}
	if got := len(backend.recordRequests()); got != 1 {
		t.Fatalf("record calls reached the backend %d times, want 1", got)
	}
	if got := len(backend.searchRequests()); got != 1 || backend.searchRequests()[0].Query != "permission denied" {
		t.Fatalf("search requests = %+v, want one query", backend.searchRequests())
	}
	if request := backend.recordRequests()[0]; request.Event.EventID != "00000000-0000-4000-8000-000000000007" {
		t.Fatalf("record reached the backend with event %q", request.Event.EventID)
	}
	if request := backend.feedbackRequests()[0]; request.Outcome != "success" || request.Note != "worked on this host" {
		t.Fatalf("feedback request = %+v", request)
	}
	if got := len(backend.solutionRequests); got != 1 || backend.solutionRequests[0].Body != "chmod the directory, then retry" {
		t.Fatalf("record_solution requests = %+v, want one with the body", backend.solutionRequests)
	}
	if got := len(backend.statusRequests); got != 1 || backend.statusRequests[0].Status != "wontfix" {
		t.Fatalf("set_status requests = %+v, want one wontfix", backend.statusRequests)
	}
	if got := len(backend.issueRequests); got != 1 || backend.issueRequests[0].IssueID != "00000000-0000-4000-8000-000000000004" {
		t.Fatalf("get_issue requests = %+v, want one for the issue", backend.issueRequests)
	}
}

// TestWriteBackToolsRejectMalformedArguments checks the two new tools fail on
// the argument boundary before any backend call.
func TestWriteBackToolsRejectMalformedArguments(t *testing.T) {
	backend := newFakeBackend()
	h := startHarness(t, backend)

	cases := []map[string]any{
		{"name": mcp.ToolRecordSolution, "arguments": map[string]any{"issue_id": "00000000-0000-4000-8000-000000000004"}},
		{"name": mcp.ToolSetStatus, "arguments": map[string]any{"issue_id": "00000000-0000-4000-8000-000000000004", "status": "fixed"}},
	}
	for index, call := range cases {
		h.send(index+1, "tools/call", call)
		frame := h.receive()
		if code := errorCodeOf(t, frame); code != -32602 {
			t.Fatalf("%v: error code = %v, want -32602", call["name"], code)
		}
	}
	if len(backend.solutionRequests) != 0 || len(backend.statusRequests) != 0 {
		t.Fatalf("invalid arguments reached the backend: %d solutions, %d statuses", len(backend.solutionRequests), len(backend.statusRequests))
	}
}

// TestStdoutCarriesOnlyProtocolFrames sends a bad frame, an unknown method and
// a ping, and proves every stdout line is JSON-RPC while logs stay on stderr.
func TestStdoutCarriesOnlyProtocolFrames(t *testing.T) {
	h := startHarness(t, newFakeBackend())

	h.sendLine("this is not json")
	badFrame := h.receive()
	if code := errorCodeOf(t, badFrame); code != -32700 {
		t.Fatalf("parse error code = %v, want -32700", code)
	}
	if badFrame["id"] != nil {
		t.Fatalf("parse error should carry a null id, got %v", badFrame["id"])
	}

	h.send(2, "no/such/method", nil)
	unknownFrame := h.receive()
	if code := errorCodeOf(t, unknownFrame); code != -32601 {
		t.Fatalf("unknown method code = %v, want -32601", code)
	}

	h.send(3, "ping", nil)
	pingFrame := h.receive()
	if id, ok := pingFrame["id"].(float64); !ok || id != 3 {
		t.Fatalf("ping answer id = %v, want 3", pingFrame["id"])
	}
	resultOf(t, pingFrame)

	logs := h.logs.String()
	if !strings.Contains(logs, "errkb mcp:") {
		t.Fatalf("server diagnostics did not reach stderr: %q", logs)
	}
}

// TestInvalidRequestsReturnProtocolErrorsAndKeepServing covers argument
// validation: a bad call is a protocol error, not a crash.
func TestInvalidRequestsReturnProtocolErrorsAndKeepServing(t *testing.T) {
	backend := newFakeBackend()
	h := startHarness(t, backend)

	h.send(1, "tools/call", map[string]any{"name": "no_such_tool", "arguments": map[string]any{}})
	if code := errorCodeOf(t, h.receive()); code != -32602 {
		t.Fatalf("unknown tool code = %v, want -32602", code)
	}

	h.send(2, "tools/call", map[string]any{
		"name":      mcp.ToolLookup,
		"arguments": map[string]any{"surprise": true},
	})
	if code := errorCodeOf(t, h.receive()); code != -32602 {
		t.Fatalf("unknown argument code = %v, want -32602", code)
	}

	h.send(3, "tools/call", map[string]any{
		"name":      mcp.ToolSearch,
		"arguments": map[string]any{"query": ""},
	})
	if code := errorCodeOf(t, h.receive()); code != -32602 {
		t.Fatalf("empty query code = %v, want -32602", code)
	}

	if got := len(backend.lookupRequests()) + len(backend.searchRequests()); got != 0 {
		t.Fatalf("invalid arguments reached the backend %d times", got)
	}

	h.send(4, "ping", nil)
	resultOf(t, h.receive())
}

// TestConcurrentCallsKeepFrameOrder pipelines five searches where the first is
// slow and proves the answers still come back in request order.
func TestConcurrentCallsKeepFrameOrder(t *testing.T) {
	backend := newFakeBackend()
	backend.setSearchDelays(200*time.Millisecond, 0, 0, 0, 0)
	h := startHarness(t, backend)

	for index := 0; index < 5; index++ {
		h.send(index+1, "tools/call", map[string]any{
			"name":      mcp.ToolSearch,
			"arguments": map[string]any{"query": fmt.Sprintf("query %d", index)},
		})
	}

	started := time.Now()
	for index := 0; index < 5; index++ {
		frame := h.receive()
		id, ok := frame["id"].(float64)
		if !ok || int(id) != index+1 {
			t.Fatalf("frame %d carries id %v, want %d (ordering broken)", index, frame["id"], index+1)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("five pipelined calls took %s", elapsed)
	}
	if got := len(backend.searchRequests()); got != 5 {
		t.Fatalf("backend saw %d searches, want 5", got)
	}
}

// TestNotificationGetsNoAnswer proves the initialize handshake's follow-up
// notification is silent.
func TestNotificationGetsNoAnswer(t *testing.T) {
	h := startHarness(t, newFakeBackend())
	h.sendNotification("notifications/initialized", nil)
	h.send(7, "ping", nil)
	frame := h.receive()
	if id, ok := frame["id"].(float64); !ok || id != 7 {
		t.Fatalf("first answer after the notification carries id %v, want 7", frame["id"])
	}
}

// TestServerStopsOnEOF covers the lifecycle: closing stdin ends Serve without
// an error and without a stray frame.
func TestServerStopsOnEOF(t *testing.T) {
	h := startHarness(t, newFakeBackend())
	h.send(1, "ping", nil)
	_ = h.receive()
	if err := h.stop(); err != nil {
		t.Fatalf("Serve returned %v after a clean EOF, want nil", err)
	}
}
