//go:build windows

// The Named Pipe client (EKB-030). CLI commands never touch PostgreSQL; they
// send one contract envelope per command to the daemon and render the answer.
// Keeping the transport here also gives EKB-031 and EKB-037 one client to reuse.
package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// ErrServiceUnavailable means the daemon could not be reached on its pipe.
var ErrServiceUnavailable = errors.New("the errkb daemon is not running")

// CallError is a business error the daemon returned in its envelope. Code is
// one of the closed set from spec/local-api.cue.
type CallError struct {
	Code      string
	Message   string
	Retryable bool
}

// Error implements error.
func (e *CallError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Client is one connection to the local API. A CLI command dials, sends what it
// needs and closes; the mutex keeps one connection's request/response pairing
// intact if a caller shares it.
type Client struct {
	name   string
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	nextID uint64
}

// newClient wraps an established connection.
func newClient(name string, conn net.Conn) *Client {
	return &Client{name: name, conn: conn, reader: bufio.NewReaderSize(conn, pipeReadBuffer)}
}

// Dial connects to the daemon on the pipe from ERRKB_PIPE, or the default when
// the variable is unset. The override lets a machine run an isolated daemon
// (tests, a second profile) without recompiling.
func Dial(ctx context.Context) (*Client, error) {
	if name := strings.TrimSpace(os.Getenv("ERRKB_PIPE")); name != "" {
		return DialPipe(ctx, name)
	}
	return DialPipe(ctx, DefaultPipeName)
}

// DialPipe connects to a specific local pipe path. An empty name means the
// default. The dial fails with ErrServiceUnavailable; it never hangs past the
// context deadline.
func DialPipe(ctx context.Context, name string) (*Client, error) {
	if name == "" {
		name = DefaultPipeName
	}
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w (pipe %s): %v", ErrServiceUnavailable, name, err)
	}
	return newClient(name, conn), nil
}

// Call sends one operation and returns the validated raw result payload. The
// envelope version, the operation, the request id and the result are checked
// with the same contract validator the server uses, so a drifted daemon cannot
// feed a command a frame the contract forbids.
func (c *Client) Call(ctx context.Context, operation string, payload any) (json.RawMessage, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode the %s payload: %w", operation, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	request := contract.PipeRequest{
		SchemaVersion: contract.PipeEnvelopeVersion,
		RequestID:     fmt.Sprintf("cli-%d-%d", os.Getpid(), c.nextID),
		Operation:     operation,
		Payload:       encoded,
	}
	frame, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode the %s request: %w", operation, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	} else {
		_ = c.conn.SetDeadline(time.Time{})
	}
	if err := writePipeFrame(c.conn, frame); err != nil {
		return nil, fmt.Errorf("%w (write to pipe %s): %v", ErrServiceUnavailable, c.name, err)
	}
	responseFrame, err := readPipeFrame(c.reader, DefaultPipeFrameBytes)
	if err != nil {
		return nil, fmt.Errorf("%w (read from pipe %s): %v", ErrServiceUnavailable, c.name, err)
	}
	response, err := contract.DecodePipeResponse(bytes.TrimSpace(responseFrame))
	if err != nil {
		return nil, fmt.Errorf("the daemon answered with a frame that breaks the local API contract: %w", err)
	}
	if response.RequestID != request.RequestID {
		return nil, fmt.Errorf("the daemon answered request %q instead of %q", response.RequestID, request.RequestID)
	}
	if response.Operation != operation {
		return nil, fmt.Errorf("the daemon answered operation %q instead of %q", response.Operation, operation)
	}
	if response.Error != nil {
		return nil, &CallError{Code: response.Error.Code, Message: response.Error.Message, Retryable: response.Error.Retryable}
	}
	return response.Result, nil
}

// Close releases the pipe connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Lookup asks "I know what this error is".
func (c *Client) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	payload, err := c.Call(ctx, contract.OperationLookup, request)
	if err != nil {
		return contract.LookupResponse{}, err
	}
	var response contract.LookupResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.LookupResponse{}, fmt.Errorf("decode the lookup result: %w", err)
	}
	return response, nil
}

// Search asks the free-text question.
func (c *Client) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	payload, err := c.Call(ctx, contract.OperationSearch, request)
	if err != nil {
		return contract.SearchResponse{}, err
	}
	var response contract.SearchResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.SearchResponse{}, fmt.Errorf("decode the search result: %w", err)
	}
	return response, nil
}

// Record submits one redacted observation.
func (c *Client) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	payload, err := c.Call(ctx, contract.OperationRecord, request)
	if err != nil {
		return contract.RecordResponse{}, err
	}
	var response contract.RecordResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.RecordResponse{}, fmt.Errorf("decode the record result: %w", err)
	}
	return response, nil
}

// Capture hands a connector observation to the core (EKB-038a). The core owns
// classification, redaction and fingerprinting, so adapters never do that work.
func (c *Client) Capture(ctx context.Context, request contract.CaptureRequest) (contract.CaptureResponse, error) {
	payload, err := c.Call(ctx, contract.OperationCapture, request)
	if err != nil {
		return contract.CaptureResponse{}, err
	}
	var response contract.CaptureResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.CaptureResponse{}, fmt.Errorf("decode the capture result: %w", err)
	}
	return response, nil
}

// Feedback submits one outcome; it cannot set trust.
func (c *Client) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	payload, err := c.Call(ctx, contract.OperationFeedback, request)
	if err != nil {
		return contract.FeedbackResponse{}, err
	}
	var response contract.FeedbackResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.FeedbackResponse{}, fmt.Errorf("decode the feedback result: %w", err)
	}
	return response, nil
}

// List pages through issues or solutions.
func (c *Client) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	payload, err := c.Call(ctx, contract.OperationList, request)
	if err != nil {
		return contract.ListResponse{}, err
	}
	var response contract.ListResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.ListResponse{}, fmt.Errorf("decode the list result: %w", err)
	}
	return response, nil
}

// Stats returns the knowledge-base summary.
func (c *Client) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	payload, err := c.Call(ctx, contract.OperationStats, request)
	if err != nil {
		return contract.StatsResponse{}, err
	}
	var response contract.StatsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.StatsResponse{}, fmt.Errorf("decode the stats result: %w", err)
	}
	return response, nil
}

// RecordSolution appends a verified solution version and resolves the issue.
func (c *Client) RecordSolution(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	payload, err := c.Call(ctx, contract.OperationRecordSolution, request)
	if err != nil {
		return contract.RecordSolutionResponse{}, err
	}
	var response contract.RecordSolutionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.RecordSolutionResponse{}, fmt.Errorf("decode the record_solution result: %w", err)
	}
	return response, nil
}

// SetStatus corrects an issue status.
func (c *Client) SetStatus(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	payload, err := c.Call(ctx, contract.OperationSetStatus, request)
	if err != nil {
		return contract.SetStatusResponse{}, err
	}
	var response contract.SetStatusResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.SetStatusResponse{}, fmt.Errorf("decode the set_status result: %w", err)
	}
	return response, nil
}

// GetIssue returns the detail document for the Web page.
func (c *Client) GetIssue(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	payload, err := c.Call(ctx, contract.OperationGetIssue, request)
	if err != nil {
		return contract.GetIssueResponse{}, err
	}
	var response contract.GetIssueResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return contract.GetIssueResponse{}, fmt.Errorf("decode the get_issue result: %w", err)
	}
	return response, nil
}

// decodePreventionResult decodes and validates one prevention response. The
// envelope decoder performs the same check; keeping it here also protects tests
// and future transports that reuse these methods without the pipe envelope.
func decodePreventionResult(payload json.RawMessage, operation string, target interface{ Validate() error }) error {
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode the %s result: %w", operation, err)
	}
	if err := target.Validate(); err != nil {
		return fmt.Errorf("validate the %s result: %w", operation, err)
	}
	return nil
}

// PreventionAdd registers artifact metadata and a draft version.
func (c *Client) PreventionAdd(ctx context.Context, request contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	payload, err := c.Call(ctx, contract.OperationPreventionAdd, request)
	if err != nil {
		return contract.PreventionAddResponse{}, err
	}
	var response contract.PreventionAddResponse
	if err := decodePreventionResult(payload, contract.OperationPreventionAdd, &response); err != nil {
		return contract.PreventionAddResponse{}, err
	}
	return response, nil
}

// PreventionVerify explicitly runs and records one verification command.
func (c *Client) PreventionVerify(ctx context.Context, request contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	payload, err := c.Call(ctx, contract.OperationPreventionVerify, request)
	if err != nil {
		return contract.PreventionVerifyResponse{}, err
	}
	var response contract.PreventionVerifyResponse
	if err := decodePreventionResult(payload, contract.OperationPreventionVerify, &response); err != nil {
		return contract.PreventionVerifyResponse{}, err
	}
	return response, nil
}

// PreventionActivate binds a verified version to the current clean Git HEAD.
func (c *Client) PreventionActivate(ctx context.Context, request contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	payload, err := c.Call(ctx, contract.OperationPreventionActivate, request)
	if err != nil {
		return contract.PreventionActivateResponse{}, err
	}
	var response contract.PreventionActivateResponse
	if err := decodePreventionResult(payload, contract.OperationPreventionActivate, &response); err != nil {
		return contract.PreventionActivateResponse{}, err
	}
	return response, nil
}

// PreventionList returns a stable page of prevention artifacts.
func (c *Client) PreventionList(ctx context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	payload, err := c.Call(ctx, contract.OperationPreventionList, request)
	if err != nil {
		return contract.PreventionListResponse{}, err
	}
	var response contract.PreventionListResponse
	if err := decodePreventionResult(payload, contract.OperationPreventionList, &response); err != nil {
		return contract.PreventionListResponse{}, err
	}
	return response, nil
}

// PreventionGet returns artifact, version and verification history.
func (c *Client) PreventionGet(ctx context.Context, request contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	payload, err := c.Call(ctx, contract.OperationPreventionGet, request)
	if err != nil {
		return contract.PreventionGetResponse{}, err
	}
	var response contract.PreventionGetResponse
	if err := decodePreventionResult(payload, contract.OperationPreventionGet, &response); err != nil {
		return contract.PreventionGetResponse{}, err
	}
	return response, nil
}
