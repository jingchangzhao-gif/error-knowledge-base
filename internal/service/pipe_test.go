//go:build windows

package service

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// fakeBackend answers every operation with a contract-valid payload and
// records the call order, so the tests prove routing rather than the database.
type fakeBackend struct {
	mu         sync.Mutex
	operations []string
	lookup     *contract.LookupResponse
	err        error
}

func (f *fakeBackend) note(operation string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.operations = append(f.operations, operation)
}

func (f *fakeBackend) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.operations...)
}

func (f *fakeBackend) Lookup(context.Context, contract.LookupRequest) (contract.LookupResponse, error) {
	f.note("lookup")
	if f.err != nil {
		return contract.LookupResponse{}, f.err
	}
	if f.lookup != nil {
		return *f.lookup, nil
	}
	return contract.LookupResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{}}, nil
}

func (f *fakeBackend) Search(context.Context, contract.SearchRequest) (contract.SearchResponse, error) {
	f.note("search")
	if f.err != nil {
		return contract.SearchResponse{}, f.err
	}
	return contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{}}, nil
}

func (f *fakeBackend) GetIssue(_ context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	f.note("get_issue")
	if f.err != nil {
		return contract.GetIssueResponse{}, f.err
	}
	return contract.GetIssueResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Issue: contract.IssueDetailV{
			ID:          request.IssueID,
			Fingerprint: "000000000000",
			Title:       "fake issue",
		},
	}, nil
}

func (f *fakeBackend) SetSolutionStatus(_ context.Context, request contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error) {
	f.note("set_solution_status")
	if f.err != nil {
		return contract.SetSolutionStatusResponse{}, f.err
	}
	return contract.SetSolutionStatusResponse{SchemaVersion: contract.ToolSchemaVersion, SolutionID: request.SolutionID, Status: request.Status}, nil
}

func (f *fakeBackend) DeleteSolution(_ context.Context, request contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error) {
	f.note("delete_solution")
	if f.err != nil {
		return contract.DeleteSolutionResponse{}, f.err
	}
	return contract.DeleteSolutionResponse{SchemaVersion: contract.ToolSchemaVersion, SolutionID: request.SolutionID, Deleted: true}, nil
}
func (f *fakeBackend) Record(_ context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	f.note("record")
	if f.err != nil {
		return contract.RecordResponse{}, f.err
	}
	return contract.RecordResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		EventID:       request.Event.EventID,
		OccurrenceID:  "occurrence-1",
		IssueID:       "issue-1",
		Fingerprint:   request.Event.Fingerprint,
		Created:       false,
	}, nil
}

func (f *fakeBackend) Feedback(_ context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	f.note("feedback")
	if f.err != nil {
		return contract.FeedbackResponse{}, f.err
	}
	return contract.FeedbackResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		OutcomeID:     request.Note,
		Recorded:      true,
	}, nil
}

func (f *fakeBackend) List(_ context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	f.note("list")
	if f.err != nil {
		return contract.ListResponse{}, f.err
	}
	return contract.ListResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Items:         []contract.ListItem{},
		NextCursor:    request.Cursor,
	}, nil
}

func (f *fakeBackend) Stats(context.Context, contract.StatsRequest) (contract.StatsResponse, error) {
	f.note("stats")
	if f.err != nil {
		return contract.StatsResponse{}, f.err
	}
	return contract.StatsResponse{SchemaVersion: contract.ToolSchemaVersion}, nil
}

func (f *fakeBackend) RecordSolution(_ context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	f.note("record_solution")
	if f.err != nil {
		return contract.RecordSolutionResponse{}, f.err
	}
	return contract.RecordSolutionResponse{
		SchemaVersion:     contract.ToolSchemaVersion,
		SolutionID:        "solution-1",
		SolutionVersionID: "solution-version-1",
		Version:           1,
		Recorded:          true,
	}, nil
}

func (f *fakeBackend) SetStatus(_ context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	f.note("set_status")
	if f.err != nil {
		return contract.SetStatusResponse{}, f.err
	}
	return contract.SetStatusResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		IssueID:       request.IssueID,
		Status:        request.Status,
	}, nil
}

func (f *fakeBackend) Capture(_ context.Context, request contract.CaptureRequest) (contract.CaptureResponse, error) {
	f.note("capture")
	if f.err != nil {
		return contract.CaptureResponse{}, f.err
	}
	return contract.CaptureResponse{
		SchemaVersion: contract.ConnectorSDKVersion,
		EventID:       "event-1",
		OccurrenceID:  "occurrence-1",
		IssueID:       "issue-1",
		Fingerprint:   "0123456789ab",
		Created:       true,
	}, nil
}

func (f *fakeBackend) PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	f.note(contract.OperationPreventionAdd)
	if f.err != nil {
		return contract.PreventionAddResponse{}, f.err
	}
	return contract.PreventionAddResponse{SchemaVersion: "v1", Artifact: preventionContractArtifact(), Version: preventionContractVersion("draft"), Created: true}, nil
}

func (f *fakeBackend) PreventionVerify(context.Context, contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	f.note(contract.OperationPreventionVerify)
	if f.err != nil {
		return contract.PreventionVerifyResponse{}, f.err
	}
	return contract.PreventionVerifyResponse{SchemaVersion: "v1", Verification: preventionContractVerification(), Version: preventionContractVersion("verified")}, nil
}

func (f *fakeBackend) PreventionActivate(context.Context, contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	f.note(contract.OperationPreventionActivate)
	if f.err != nil {
		return contract.PreventionActivateResponse{}, f.err
	}
	return contract.PreventionActivateResponse{SchemaVersion: "v1", Version: preventionContractVersion("active"), Activated: true}, nil
}

func (f *fakeBackend) PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	f.note(contract.OperationPreventionList)
	if f.err != nil {
		return contract.PreventionListResponse{}, f.err
	}
	return contract.PreventionListResponse{SchemaVersion: "v1", Items: []contract.PreventionArtifactView{}}, nil
}

func (f *fakeBackend) PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	f.note(contract.OperationPreventionGet)
	if f.err != nil {
		return contract.PreventionGetResponse{}, f.err
	}
	return contract.PreventionGetResponse{SchemaVersion: "v1", Artifact: preventionContractArtifact(), Versions: []contract.PreventionVersion{}, Verifications: []contract.PreventionVerification{}}, nil
}

func newTestPipeName() string {
	return `\\.\pipe\errkb-test-` + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// startTestPipe starts a listener on a unique name and fails the test if the
// accept loop returns an unexpected error.
func startTestPipe(t *testing.T, options PipeOptions) (*PipeServer, string) {
	t.Helper()
	if options.Name == "" {
		options.Name = newTestPipeName()
	}
	server, err := ListenPipe(options)
	if err != nil {
		t.Fatalf("listen on the test pipe: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(context.Background()) }()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("the pipe accept loop failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("the pipe accept loop did not stop after Close")
		}
	})
	return server, options.Name
}

// dialTestPipe connects to the pipe with a short deadline so a missing server
// fails the test instead of hanging it.
func dialTestPipe(t *testing.T, name string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		t.Fatalf("dial the test pipe: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// writeTestFrame sends one JSON line.
func writeTestFrame(t *testing.T, conn net.Conn, frame string) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set the write deadline: %v", err)
	}
	if _, err := conn.Write([]byte(frame + "\n")); err != nil {
		t.Fatalf("write a test frame: %v", err)
	}
}

// readTestFrame reads one JSON line.
func readTestFrame(t *testing.T, conn net.Conn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set the read deadline: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read a test frame: %v", err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
}

// callTestPipe sends one frame and returns the raw response frame.
func callTestPipe(t *testing.T, conn net.Conn, frame string) string {
	t.Helper()
	writeTestFrame(t, conn, frame)
	return readTestFrame(t, conn)
}

// decodeResponse parses a response with the same validator a client uses.
func decodeResponse(t *testing.T, frame string) contract.PipeResponse {
	t.Helper()
	response, err := contract.DecodePipeResponse([]byte(frame))
	if err != nil {
		t.Fatalf("decode the response frame %q: %v", frame, err)
	}
	return response
}

// decodeRejection parses an error frame leniently: rejection frames are not
// required to echo a valid operation, and some cannot.
type rejection struct {
	RequestID string              `json:"request_id"`
	Operation string              `json:"operation"`
	Error     *contract.PipeError `json:"error"`
}

func decodeRejection(t *testing.T, frame string) rejection {
	t.Helper()
	var rejected rejection
	if err := json.Unmarshal([]byte(frame), &rejected); err != nil {
		t.Fatalf("decode the rejection frame %q: %v", frame, err)
	}
	if rejected.Error == nil {
		t.Fatalf("the frame %q is not an error response", frame)
	}
	return rejected
}

// requestFrame builds one envelope frame around a payload.
func requestFrame(t *testing.T, requestID, operation string, payload any) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode the %s payload: %v", operation, err)
	}
	frame, err := json.Marshal(contract.PipeRequest{
		SchemaVersion: contract.PipeEnvelopeVersion,
		RequestID:     requestID,
		Operation:     operation,
		Payload:       encoded,
	})
	if err != nil {
		t.Fatalf("encode the %s frame: %v", operation, err)
	}
	return string(frame)
}

// statsPayload is the smallest valid payload of each read operation.
func statsPayload() contract.StatsRequest {
	return contract.StatsRequest{SchemaVersion: contract.ToolSchemaVersion}
}

func listPayload(cursor string) contract.ListRequest {
	return contract.ListRequest{SchemaVersion: contract.ToolSchemaVersion, Kind: "issue", Cursor: cursor}
}

func feedbackPayload(note string) contract.FeedbackRequest {
	return contract.FeedbackRequest{
		SchemaVersion:     contract.ToolSchemaVersion,
		IssueID:           "issue-1",
		SolutionVersionID: "solution-version-1",
		Outcome:           "success",
		Note:              note,
	}
}

func lookupPayload(fingerprint string) contract.LookupRequest {
	return contract.LookupRequest{SchemaVersion: contract.ToolSchemaVersion, Fingerprint: fingerprint}
}

func searchPayload(query string) contract.SearchRequest {
	return contract.SearchRequest{SchemaVersion: contract.ToolSchemaVersion, Query: query}
}

func recordPayload(eventID string) contract.RecordRequest {
	transient := false
	confidence := 0.5
	return contract.RecordRequest{
		SchemaVersion: contract.ToolSchemaVersion,
		Event: contract.ErrorEvent{
			SchemaVersion:     contract.SchemaVersion,
			EventID:           eventID,
			ConnectorID:       "connector-1",
			SessionID:         "session-1",
			ObservedAt:        "2026-10-19T10:00:00Z",
			Channel:           "A",
			Category:          "tool",
			Severity:          "error",
			Transient:         &transient,
			Headline:          "eperm rename failed",
			NormalizedMessage: "eperm rename failed",
			RedactedSample:    "eperm rename failed",
			OS:                "windows",
			ProjectKey:        "proj-1",
			Fingerprint:       "0123456789ab",
			Evidence:          "rename failed in the package directory",
			Confidence:        &confidence,
		},
	}
}

func TestPipeRoutesEveryOperation(t *testing.T) {
	backend := &fakeBackend{}
	api := NewAPI(backend)
	_, name := startTestPipe(t, PipeOptions{API: api})
	conn := dialTestPipe(t, name)

	cases := []struct {
		operation string
		payload   any
	}{
		{"lookup", lookupPayload("0123456789ab")},
		{"search", searchPayload("pinpoint")},
		{"record", recordPayload("6f0c1a76-5a5e-4a6f-9d37-2b8f0b0f5b21")},
		{"feedback", feedbackPayload("note-1")},
		{"list", listPayload("page-2")},
		{"stats", statsPayload()},
		{"capture", contract.CaptureRequest{
			SchemaVersion: contract.ConnectorSDKVersion,
			ConnectorID:   "adapter",
			SessionID:     "session-1",
			Observation: contract.ConnectorObservation{
				ObservedAt: "2026-10-19T10:00:00Z",
				Source:     "tool",
				Message:    "eperm rename failed",
			},
		}},
		{"record_solution", contract.RecordSolutionRequest{
			SchemaVersion: contract.ToolSchemaVersion,
			IssueID:       "11111111-1111-4111-8111-111111111111",
			Body:          "chmod the directory, then retry",
		}},
		{"set_status", contract.SetStatusRequest{
			SchemaVersion: contract.ToolSchemaVersion,
			IssueID:       "11111111-1111-4111-8111-111111111111",
			Status:        "resolved",
		}},
	}
	for _, test := range cases {
		requestID := "route-" + test.operation
		response := decodeResponse(t, callTestPipe(t, conn, requestFrame(t, requestID, test.operation, test.payload)))
		if response.RequestID != requestID || response.Operation != test.operation {
			t.Fatalf("the %s response did not echo its request identity: %+v", test.operation, response)
		}
		if len(response.Result) == 0 || response.Error != nil {
			t.Fatalf("the %s response did not carry a result: %+v", test.operation, response)
		}
	}
	want := []string{"lookup", "search", "record", "feedback", "list", "stats", "capture", "record_solution", "set_status"}
	got := backend.calls()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("routed operations = %v, want %v", got, want)
	}
}

func TestPipeRejectsRequestsWithoutSchemaVersion(t *testing.T) {
	backend := &fakeBackend{}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	conn := dialTestPipe(t, name)

	frame := `{"request_id":"no-version","operation":"stats","payload":{"schema_version":"v1"}}`
	rejected := decodeRejection(t, callTestPipe(t, conn, frame))
	if rejected.Error.Code != CodeInvalidRequest {
		t.Fatalf("missing schema_version code = %q, want %q", rejected.Error.Code, CodeInvalidRequest)
	}
	if !strings.Contains(rejected.Error.Message, "schema_version") {
		t.Fatalf("missing schema_version message = %q, want it to name the field", rejected.Error.Message)
	}
	if rejected.RequestID != "no-version" {
		t.Fatalf("rejection request_id = %q, want the envelope id", rejected.RequestID)
	}
	if len(backend.calls()) != 0 {
		t.Fatalf("a request without schema_version reached the backend: %v", backend.calls())
	}
}

func TestPipeRejectsIncompatibleVersions(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	conn := dialTestPipe(t, name)

	envelope := `{"schema_version":"v2","request_id":"old-envelope","operation":"stats","payload":{"schema_version":"v1"}}`
	rejected := decodeRejection(t, callTestPipe(t, conn, envelope))
	if rejected.Error.Code != CodeInvalidRequest || !strings.Contains(rejected.Error.Message, `want "v1"`) {
		t.Fatalf("envelope v2 rejection = %+v, want invalid_request naming v1", rejected)
	}

	payload := `{"schema_version":"v1","request_id":"old-payload","operation":"stats","payload":{"schema_version":"v2"}}`
	rejected = decodeRejection(t, callTestPipe(t, conn, payload))
	if rejected.Error.Code != CodeInvalidRequest || !strings.Contains(rejected.Error.Message, "stats.schema_version") {
		t.Fatalf("payload v2 rejection = %+v, want invalid_request naming the operation version", rejected)
	}
}

func TestPipeRejectsUnknownOperation(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	conn := dialTestPipe(t, name)

	frame := `{"schema_version":"v1","request_id":"unknown-op","operation":"delete","payload":{"schema_version":"v1"}}`
	rejected := decodeRejection(t, callTestPipe(t, conn, frame))
	if rejected.Error.Code != CodeInvalidRequest || !strings.Contains(rejected.Error.Message, "unknown operation") {
		t.Fatalf("unknown operation rejection = %+v", rejected)
	}
	if rejected.RequestID != "unknown-op" {
		t.Fatalf("unknown operation request_id = %q, want the envelope id", rejected.RequestID)
	}
}

func TestPipeRejectsMalformedFrames(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	conn := dialTestPipe(t, name)

	for _, frame := range []string{"not json", `{"schema_version":"v1"}`, `{"request_id":"x","operation":"stats","payload":{"schema_version":"v1"} trailing`} {
		rejected := decodeRejection(t, callTestPipe(t, conn, frame))
		if rejected.Error.Code != CodeInvalidRequest {
			t.Fatalf("frame %q code = %q, want invalid_request", frame, rejected.Error.Code)
		}
	}
}

func TestPipeRejectsOversizedFrames(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{}), MaxFrameBytes: 512})
	conn := dialTestPipe(t, name)

	writeTestFrame(t, conn, strings.Repeat("a", 4096))
	rejected := decodeRejection(t, readTestFrame(t, conn))
	if rejected.Error.Code != CodeInvalidRequest || !strings.Contains(rejected.Error.Message, "512") {
		t.Fatalf("oversized frame rejection = %+v, want invalid_request naming the cap", rejected)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatalf("the connection stayed open after an oversized frame")
	}
}

func TestPipeClientFailsFastWhenTheServerIsAbsent(t *testing.T) {
	name := `\\.\pipe\errkb-absent-` + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := winio.DialPipeContext(ctx, name)
	elapsed := time.Since(start)
	if conn != nil {
		_ = conn.Close()
		t.Fatalf("dialed a pipe that does not exist")
	}
	if err == nil {
		t.Fatalf("dialing a missing pipe returned no error")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("dialing a missing pipe took %s; it must fail fast instead of hanging", elapsed)
	}
}

func TestPipeConcurrentConnectionsDoNotInterleave(t *testing.T) {
	backend := &fakeBackend{}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})

	const connections = 6
	const framesPerConnection = 8
	var wait sync.WaitGroup
	failures := make(chan string, connections*framesPerConnection)
	for connectionIndex := 0; connectionIndex < connections; connectionIndex++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			conn, err := winio.DialPipeContext(context.Background(), name)
			if err != nil {
				failures <- fmt.Sprintf("connection %d: dial: %v", index, err)
				return
			}
			defer func() { _ = conn.Close() }()
			for frameIndex := 0; frameIndex < framesPerConnection; frameIndex++ {
				note := fmt.Sprintf("c%d-r%d", index, frameIndex)
				request := requestFrame(t, note, "feedback", feedbackPayload(note))
				if _, err := conn.Write([]byte(request + "\n")); err != nil {
					failures <- fmt.Sprintf("connection %d frame %d: write: %v", index, frameIndex, err)
					return
				}
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					failures <- fmt.Sprintf("connection %d frame %d: read: %v", index, frameIndex, err)
					return
				}
				response, err := contract.DecodePipeResponse([]byte(strings.TrimSpace(line)))
				if err != nil {
					failures <- fmt.Sprintf("connection %d frame %d: decode: %v", index, frameIndex, err)
					return
				}
				var result contract.FeedbackResponse
				if err := json.Unmarshal(response.Result, &result); err != nil {
					failures <- fmt.Sprintf("connection %d frame %d: result: %v", index, frameIndex, err)
					return
				}
				if response.RequestID != note || result.OutcomeID != note {
					failures <- fmt.Sprintf("connection %d frame %d got request %q outcome %q; frames interleaved", index, frameIndex, response.RequestID, result.OutcomeID)
					return
				}
			}
		}(connectionIndex)
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

func TestPipeSerializesFramesOnOneConnection(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	conn := dialTestPipe(t, name)

	for index := 0; index < 25; index++ {
		requestID := "serial-" + strconv.Itoa(index)
		response := decodeResponse(t, callTestPipe(t, conn, requestFrame(t, requestID, "list", listPayload(requestID))))
		if response.RequestID != requestID {
			t.Fatalf("request %d got response id %q", index, response.RequestID)
		}
	}
}

func TestPipeSurvivesAClientDisconnect(t *testing.T) {
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})

	first := dialTestPipe(t, name)
	response := decodeResponse(t, callTestPipe(t, first, requestFrame(t, "before-close", "stats", statsPayload())))
	if response.RequestID != "before-close" {
		t.Fatalf("first connection response id = %q", response.RequestID)
	}
	_ = first.Close()

	second := dialTestPipe(t, name)
	response = decodeResponse(t, callTestPipe(t, second, requestFrame(t, "after-close", "stats", statsPayload())))
	if response.RequestID != "after-close" {
		t.Fatalf("second connection response id = %q", response.RequestID)
	}
}

func TestPipeBackendErrorsCarryTheContractCodes(t *testing.T) {
	backend := &fakeBackend{err: &APIError{Code: CodeNotFound, Message: "no issue with that fingerprint"}}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	conn := dialTestPipe(t, name)

	rejected := decodeRejection(t, callTestPipe(t, conn, requestFrame(t, "missing", "lookup", lookupPayload("0123456789ab"))))
	if rejected.Error.Code != CodeNotFound || rejected.Error.Message != "no issue with that fingerprint" || rejected.Error.Retryable {
		t.Fatalf("APIError mapping = %+v, want not_found without retry", rejected.Error)
	}

	backend.err = context.DeadlineExceeded
	rejected = decodeRejection(t, callTestPipe(t, conn, requestFrame(t, "timeout", "lookup", lookupPayload("0123456789ab"))))
	if rejected.Error.Code != CodeUnavailable || !rejected.Error.Retryable {
		t.Fatalf("deadline mapping = %+v, want retryable unavailable", rejected.Error)
	}

	backend.err = errors.New("boom")
	rejected = decodeRejection(t, callTestPipe(t, conn, requestFrame(t, "boom", "lookup", lookupPayload("0123456789ab"))))
	if rejected.Error.Code != CodeInternal || !strings.Contains(rejected.Error.Message, "boom") {
		t.Fatalf("generic mapping = %+v, want internal carrying the message", rejected.Error)
	}
}

func TestPipeRejectsAResponseThatBreaksTheContract(t *testing.T) {
	// The backend answers a valid envelope with the wrong payload version; the
	// dispatcher must catch it before the frame reaches the wire.
	backend := &fakeBackend{lookup: &contract.LookupResponse{SchemaVersion: "v9", Candidates: []contract.Candidate{}}}
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(backend)})
	conn := dialTestPipe(t, name)

	rejected := decodeRejection(t, callTestPipe(t, conn, requestFrame(t, "drift", "lookup", lookupPayload("0123456789ab"))))
	if rejected.Error.Code != CodeInternal || !strings.Contains(rejected.Error.Message, "violates the local API contract") {
		t.Fatalf("contract drift = %+v, want internal naming the contract", rejected.Error)
	}
}

func TestPipeBeginRequestHookRejectsWhenTheDaemonIsClosing(t *testing.T) {
	backend := &fakeBackend{}
	_, name := startTestPipe(t, PipeOptions{
		API: NewAPI(backend),
		BeginRequest: func() (func(), error) {
			return nil, ErrShuttingDown
		},
	})
	conn := dialTestPipe(t, name)

	rejected := decodeRejection(t, callTestPipe(t, conn, requestFrame(t, "closing", "stats", statsPayload())))
	if rejected.Error.Code != CodeUnavailable || !rejected.Error.Retryable {
		t.Fatalf("shutdown rejection = %+v, want retryable unavailable", rejected.Error)
	}
	if len(backend.calls()) != 0 {
		t.Fatalf("a request reached the backend while the daemon was closing")
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatalf("the connection stayed open after the daemon began closing")
	}
}

func TestPipeRejectsANonPipeName(t *testing.T) {
	if _, err := ListenPipe(PipeOptions{Name: "errkb", API: NewAPI(&fakeBackend{})}); err == nil {
		t.Fatalf("ListenPipe accepted a name outside \\\\.\\pipe\\")
	}
	if _, err := ListenPipe(PipeOptions{}); err == nil {
		t.Fatalf("ListenPipe accepted a nil API")
	}
}

// currentUserSID returns the SID the pipe ACL is built from.
func currentUserSID(t *testing.T) string {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatalf("open the current process token: %v", err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatalf("read the current user: %v", err)
	}
	return user.User.Sid.String()
}

// pipeDACL connects to the pipe once and reads the DACL of its kernel object
// back as SDDL. This is the actual ACL Windows enforces, not the input string.
func pipeDACL(t *testing.T, name string) string {
	t.Helper()
	// A raw CreateFile does not retry ERROR_PIPE_BUSY the way the go-winio
	// dialer does, so connect through the dialer and read the DACL from the
	// connected handle; a named pipe's instances share the descriptor.
	conn := dialTestPipe(t, name)
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		t.Fatalf("the pipe connection does not expose a Windows handle")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(fd.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the pipe security descriptor: %v", err)
	}
	return sd.String()
}

// sddlSIDs resolves every allow ACE in a self-relative security descriptor to a
// SID string. Windows canonicalizes the input SID to an SDDL alias (for an
// Administrator account it prints LA), so the live check must compare SIDs, not
// the text we passed in.
func sddlSIDs(t *testing.T, sddl string) []string {
	t.Helper()
	raw, err := winio.SddlToSecurityDescriptor(sddl)
	if err != nil {
		t.Fatalf("parse the live security descriptor %q: %v", sddl, err)
	}
	if len(raw) < 20 {
		t.Fatalf("the security descriptor is only %d bytes", len(raw))
	}
	control := binary.LittleEndian.Uint16(raw[2:4])
	if control&0x8000 == 0 {
		t.Fatalf("the security descriptor is not self-relative")
	}
	daclOffset := int(binary.LittleEndian.Uint32(raw[16:20]))
	if daclOffset == 0 || daclOffset >= len(raw) {
		return nil
	}
	dacl := raw[daclOffset:]
	aceCount := int(binary.LittleEndian.Uint16(dacl[4:6]))
	offset := 8
	sids := make([]string, 0, aceCount)
	for index := 0; index < aceCount; index++ {
		if offset+8 > len(dacl) {
			t.Fatalf("the DACL ended before its declared ACE count")
		}
		ace := dacl[offset:]
		aceSize := int(binary.LittleEndian.Uint16(ace[2:4]))
		if aceSize < 8 {
			t.Fatalf("ACE %d has size %d", index, aceSize)
		}
		switch ace[0] {
		case 0x00: // ACCESS_ALLOWED_ACE
			sids = append(sids, sidString(t, ace[8:]))
		case 0x01: // ACCESS_DENIED_ACE
			t.Fatalf("the pipe DACL contains a deny ACE, which this test does not model")
		}
		offset += aceSize
	}
	return sids
}

// sidString formats a binary SID as S-1-<authority>-<subauths>.
func sidString(t *testing.T, sid []byte) string {
	t.Helper()
	if len(sid) < 8 {
		t.Fatalf("a SID is only %d bytes", len(sid))
	}
	revision := int(sid[0])
	count := int(sid[1])
	if len(sid) < 8+4*count {
		t.Fatalf("a SID declares %d sub-authorities but is %d bytes", count, len(sid))
	}
	authority := uint64(sid[2])<<40 | uint64(sid[3])<<32 | uint64(sid[4])<<24 |
		uint64(sid[5])<<16 | uint64(sid[6])<<8 | uint64(sid[7])
	parts := []string{"S", strconv.Itoa(revision), strconv.FormatUint(authority, 10)}
	for index := 0; index < count; index++ {
		parts = append(parts, strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sid[8+4*index:])), 10))
	}
	return strings.Join(parts, "-")
}

func TestPipeACLIsOwnerOnly(t *testing.T) {
	sid := currentUserSID(t)
	server, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})

	descriptor := server.SecurityDescriptor()
	if !strings.Contains(descriptor, ";;;"+sid+")") {
		t.Fatalf("the pipe DACL does not grant the current user %s: %s", sid, descriptor)
	}
	if !strings.Contains(descriptor, ";;;SY)") {
		t.Fatalf("the pipe DACL does not grant SYSTEM: %s", descriptor)
	}
	for _, everyone := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;AN)"} {
		if strings.Contains(descriptor, everyone) {
			t.Fatalf("the pipe DACL grants %s: %s", everyone, descriptor)
		}
	}

	actual := pipeDACL(t, name)
	granted := sddlSIDs(t, actual)
	want := []string{"S-1-5-18", sid} // SYSTEM and the current user, nothing else
	sort.Strings(granted)
	sort.Strings(want)
	if strings.Join(granted, ",") != strings.Join(want, ",") {
		t.Fatalf("the live pipe DACL grants %v, want exactly %v (raw %s)", granted, want, actual)
	}
}

// tcpListenersForThisProcess lists every listening TCP endpoint owned by the
// test process, so the no-TCP assertion is about the process, not about a port
// that might happen to be free.
func tcpListenersForThisProcess(t *testing.T) []string {
	t.Helper()
	iphlpapi := windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable := iphlpapi.NewProc("GetExtendedTcpTable")
	const tcpTableOwnerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER
	const afInet = 2

	var size uint32
	result, _, callErr := getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListener, 0)
	if result != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		t.Skipf("GetExtendedTcpTable sized the table unexpectedly (%d, %v)", result, callErr)
	}
	buffer := make([]byte, size)
	result, _, callErr = getExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDListener, 0)
	if result != 0 {
		t.Skipf("GetExtendedTcpTable failed (%d, %v)", result, callErr)
	}
	count := binary.LittleEndian.Uint32(buffer[:4])
	rows := make([]string, 0, count)
	const rowSize = 24 // six DWORDs: state, local addr/port, remote addr/port, pid
	for index := uint32(0); index < count; index++ {
		offset := 4 + int(index)*rowSize
		if offset+rowSize > len(buffer) {
			t.Fatalf("the TCP table is shorter than its own row count")
		}
		row := buffer[offset : offset+rowSize]
		localAddress := binary.LittleEndian.Uint32(row[4:8])
		localPort := binary.BigEndian.Uint16(row[8:10])
		ownerPID := binary.LittleEndian.Uint32(row[20:24])
		if ownerPID != uint32(os.Getpid()) {
			continue
		}
		address := net.IPv4(byte(localAddress), byte(localAddress>>8), byte(localAddress>>16), byte(localAddress>>24))
		rows = append(rows, net.JoinHostPort(address.String(), strconv.Itoa(int(localPort))))
	}
	return rows
}

func TestPipeOpensNoTCPListener(t *testing.T) {
	// Sanity-check the table reader itself: a listener this process opens on
	// purpose must show up, otherwise the assertion below would be vacuous.
	sanity, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("open the sanity TCP listener: %v", err)
	}
	sanityEndpoint := sanity.Addr().String()
	listeners := tcpListenersForThisProcess(t)
	sanity.Close()
	found := false
	for _, endpoint := range listeners {
		if endpoint == sanityEndpoint {
			found = true
		}
	}
	if !found {
		t.Fatalf("the TCP table reader did not see our own listener %s (saw %v)", sanityEndpoint, listeners)
	}

	before := tcpListenersForThisProcess(t)
	_, name := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	conn := dialTestPipe(t, name)
	response := decodeResponse(t, callTestPipe(t, conn, requestFrame(t, "tcp-check", "stats", statsPayload())))
	if response.RequestID != "tcp-check" {
		t.Fatalf("the pipe did not answer while checking for TCP listeners")
	}
	after := tcpListenersForThisProcess(t)
	beforeSet := make(map[string]bool, len(before))
	for _, endpoint := range before {
		beforeSet[endpoint] = true
	}
	for _, endpoint := range after {
		if !beforeSet[endpoint] {
			t.Fatalf("the pipe listener opened a TCP listener at %s", endpoint)
		}
	}
}

// TestPipeLayerHasNoTCPLoggingOrBusinessLogic pins the card's non-functional
// rules structurally: no HTTP import, no listen call, no logging of captured
// text, and no storage import in the dispatcher.
func TestPipeLayerHasNoTCPLoggingOrBusinessLogic(t *testing.T) {
	files := map[string]string{"pipe.go": "", "api.go": ""}
	for fileName := range files {
		source, err := os.ReadFile(fileName)
		if err != nil {
			t.Fatalf("read %s: %v", fileName, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), fileName, source, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", fileName, err)
		}
		for _, imported := range parsed.Imports {
			path := strings.Trim(imported.Path.Value, `"`)
			if path == "net/http" {
				t.Fatalf("%s imports net/http; the local API must not offer TCP", fileName)
			}
			if path == "github.com/jingchangzhao-gif/error-knowledge-base/internal/database" || strings.Contains(path, "pgx") {
				t.Fatalf("%s imports the storage layer; the dispatcher must not carry business logic", fileName)
			}
		}

		full, err := parser.ParseFile(token.NewFileSet(), fileName, source, 0)
		if err != nil {
			t.Fatalf("parse %s with bodies: %v", fileName, err)
		}
		ast.Inspect(full, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Listen", "ListenPacket", "ListenAndServe", "ListenAndServeTLS", "Print", "Printf", "Println", "Fprintf", "Fprintln":
				t.Fatalf("%s calls %s; the pipe layer must not listen on TCP or log captured text", fileName, selector.Sel.Name)
			}
			return true
		})
	}
}
