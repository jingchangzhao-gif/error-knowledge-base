// The local API envelope dispatcher (EKB-029). pipe.go owns the Named Pipe
// transport; this file only validates the versioned envelope, routes one of the
// seven operations to the Backend and encodes the answer.
//
// It deliberately imports no storage package. The card forbids business logic
// in the pipe layer: a request is decoded, checked against the contract and
// handed to a Backend implementation. EKB-030 and EKB-037 wire the real backend;
// tests wire fakes. The capture operation (EKB-038) is a local-API-only
// connector SDK call, never an MCP tool.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// Backend is the business surface the local API routes to. Every method
// receives an already validated request and answers with a payload the
// contract accepts; the dispatcher still re-decodes the answer, so a drifted
// backend fails the request instead of emitting an invalid frame.
type Backend interface {
	Lookup(context.Context, contract.LookupRequest) (contract.LookupResponse, error)
	Search(context.Context, contract.SearchRequest) (contract.SearchResponse, error)
	Record(context.Context, contract.RecordRequest) (contract.RecordResponse, error)
	Feedback(context.Context, contract.FeedbackRequest) (contract.FeedbackResponse, error)
	List(context.Context, contract.ListRequest) (contract.ListResponse, error)
	Stats(context.Context, contract.StatsRequest) (contract.StatsResponse, error)
	// Capture is the connector SDK call: it accepts a raw observation and the
	// core does classification, redaction, fingerprinting and storage.
	Capture(context.Context, contract.CaptureRequest) (contract.CaptureResponse, error)
	// RecordSolution appends a verified solution version and resolves the issue.
	RecordSolution(context.Context, contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error)
	// GetIssue returns one issue with its occurrence, solution and outcome history.
	GetIssue(context.Context, contract.GetIssueRequest) (contract.GetIssueResponse, error)
	// SetStatus corrects an issue status (open/resolved/wontfix/misjudged).
	SetStatus(context.Context, contract.SetStatusRequest) (contract.SetStatusResponse, error)
	// SetSolutionStatus moves a solution through its closed state machine.
	SetSolutionStatus(context.Context, contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error)
	// DeleteSolution removes a solution and its versions after confirmation.
	DeleteSolution(context.Context, contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error)
	PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error)
	PreventionVerify(context.Context, contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error)
	PreventionActivate(context.Context, contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error)
	PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error)
	PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error)
}

// The error codes are the closed set from spec/local-api.cue. A caller must be
// able to branch on them: invalid_request means "fix the request", unavailable
// is the only retryable transport-ish code, internal means a core bug.
const (
	CodeInvalidRequest = "invalid_request"
	CodeNotFound       = "not_found"
	CodeUnavailable    = "unavailable"
	CodeConflict       = "conflict"
	CodeInternal       = "internal"
)

// APIError is a backend failure carrying one of the contract error codes to the
// wire. A backend returns it; anything else becomes CodeInternal.
type APIError struct {
	Code      string
	Message   string
	Retryable bool
}

// Error implements error.
func (e *APIError) Error() string { return e.Message }

// APIErrorf builds an APIError with a formatted message.
func APIErrorf(code, format string, args ...any) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// API routes decoded envelope frames to a Backend. It is safe for concurrent
// use: Handle keeps no per-request state.
type API struct {
	backend Backend
}

// NewAPI wires a backend. A nil backend answers every operation with
// CodeUnavailable instead of panicking.
func NewAPI(backend Backend) *API {
	return &API{backend: backend}
}

// Handle answers exactly one request frame with exactly one response frame.
// Both frames are newline-free JSON; the transport adds the framing.
//
// The envelope version and the payload version are both checked by
// contract.DecodePipeRequest against the EKB-011 baseline: an old or missing
// version is rejected with CodeInvalidRequest instead of being interpreted
// under today's semantics.
func (a *API) Handle(ctx context.Context, frame []byte) []byte {
	request, err := contract.DecodePipeRequest(frame)
	if err != nil {
		requestID, operation := peekRequestIdentity(frame)
		return a.rejectFrame(requestID, operation, APIErrorf(CodeInvalidRequest, "%v", err))
	}
	if a.backend == nil {
		return a.rejectFrame(request.RequestID, request.Operation,
			APIErrorf(CodeUnavailable, "no backend is configured for the local API"))
	}
	switch request.Operation {
	case contract.OperationLookup:
		var payload contract.LookupRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the lookup payload: %v", err))
		}
		response, err := a.backend.Lookup(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationSearch:
		var payload contract.SearchRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the search payload: %v", err))
		}
		response, err := a.backend.Search(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationRecord:
		var payload contract.RecordRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the record payload: %v", err))
		}
		response, err := a.backend.Record(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationFeedback:
		var payload contract.FeedbackRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the feedback payload: %v", err))
		}
		response, err := a.backend.Feedback(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationList:
		var payload contract.ListRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the list payload: %v", err))
		}
		response, err := a.backend.List(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationStats:
		var payload contract.StatsRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the stats payload: %v", err))
		}
		response, err := a.backend.Stats(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationCapture:
		var payload contract.CaptureRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the capture payload: %v", err))
		}
		response, err := a.backend.Capture(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationRecordSolution:
		var payload contract.RecordSolutionRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the record_solution payload: %v", err))
		}
		response, err := a.backend.RecordSolution(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationGetIssue:
		var payload contract.GetIssueRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the get_issue payload: %v", err))
		}
		response, err := a.backend.GetIssue(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationSetSolutionStatus:
		var payload contract.SetSolutionStatusRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the set_solution_status payload: %v", err))
		}
		response, err := a.backend.SetSolutionStatus(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationDeleteSolution:
		var payload contract.DeleteSolutionRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the delete_solution payload: %v", err))
		}
		response, err := a.backend.DeleteSolution(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationSetStatus:
		var payload contract.SetStatusRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation,
				APIErrorf(CodeInvalidRequest, "decode the set_status payload: %v", err))
		}
		response, err := a.backend.SetStatus(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationPreventionAdd:
		var payload contract.PreventionAddRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation, APIErrorf(CodeInvalidRequest, "decode the prevention_add payload: %v", err))
		}
		response, err := a.backend.PreventionAdd(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationPreventionVerify:
		var payload contract.PreventionVerifyRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation, APIErrorf(CodeInvalidRequest, "decode the prevention_verify payload: %v", err))
		}
		response, err := a.backend.PreventionVerify(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationPreventionActivate:
		var payload contract.PreventionActivateRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation, APIErrorf(CodeInvalidRequest, "decode the prevention_activate payload: %v", err))
		}
		response, err := a.backend.PreventionActivate(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationPreventionList:
		var payload contract.PreventionListRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation, APIErrorf(CodeInvalidRequest, "decode the prevention_list payload: %v", err))
		}
		response, err := a.backend.PreventionList(ctx, payload)
		return a.encodeResult(request, response, err)
	case contract.OperationPreventionGet:
		var payload contract.PreventionGetRequest
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return a.rejectFrame(request.RequestID, request.Operation, APIErrorf(CodeInvalidRequest, "decode the prevention_get payload: %v", err))
		}
		response, err := a.backend.PreventionGet(ctx, payload)
		return a.encodeResult(request, response, err)
	default:
		// DecodePipeRequest already rejects unknown operations; this branch is
		// unreachable unless the contract's operation list changes.
		return a.rejectFrame(request.RequestID, request.Operation,
			APIErrorf(CodeInvalidRequest, "unknown operation %q", request.Operation))
	}
}

// encodeResult wraps a backend result in the response envelope. The encoded
// frame is decoded again with the client-side contract validator before it is
// sent: a backend that breaks the contract fails the request with CodeInternal
// instead of putting a frame on the wire that the caller cannot read.
func (a *API) encodeResult(request contract.PipeRequest, result any, backendErr error) []byte {
	if backendErr != nil {
		return a.rejectFrame(request.RequestID, request.Operation, backendErr)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return a.rejectFrame(request.RequestID, request.Operation,
			APIErrorf(CodeInternal, "encode the %s result: %v", request.Operation, err))
	}
	frame := contract.PipeResponse{
		SchemaVersion: contract.PipeEnvelopeVersion,
		RequestID:     request.RequestID,
		Operation:     request.Operation,
		Result:        payload,
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return a.rejectFrame(request.RequestID, request.Operation,
			APIErrorf(CodeInternal, "encode the %s response: %v", request.Operation, err))
	}
	if _, err := contract.DecodePipeResponse(encoded); err != nil {
		return a.rejectFrame(request.RequestID, request.Operation,
			APIErrorf(CodeInternal, "the %s result violates the local API contract: %v", request.Operation, err))
	}
	return encoded
}

// rejectFrame answers a request that never reached the backend. The request id
// and operation are echoed when they could be read, so a client can line the
// error up with its call; a frame that could not be read at all says
// request_id "invalid". The operation is echoed verbatim, including an unknown
// one, because that is the only way to say which operation was rejected.
func (a *API) rejectFrame(requestID, operation string, err error) []byte {
	pipeError := pipeErrorFrom(err)
	if requestID == "" {
		requestID = "invalid"
	}
	frame := contract.PipeResponse{
		SchemaVersion: contract.PipeEnvelopeVersion,
		RequestID:     requestID,
		Operation:     operation,
		Error:         &pipeError,
	}
	encoded, marshalErr := json.Marshal(frame)
	if marshalErr != nil {
		// The envelope is plain strings, so this cannot happen; keep a literal
		// fallback rather than returning a nil frame to the transport.
		return []byte(`{"schema_version":"v1","request_id":"invalid","operation":"","error":{"code":"internal","message":"encode the error response","retryable":false}}`)
	}
	return encoded
}

// pipeErrorFrom maps a backend error to the closed wire error set.
func pipeErrorFrom(err error) contract.PipeError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return contract.PipeError{Code: apiErr.Code, Message: apiErr.Message, Retryable: apiErr.Retryable}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return contract.PipeError{
			Code:      CodeUnavailable,
			Message:   "the request was cancelled before the core answered",
			Retryable: true,
		}
	case errors.Is(err, context.DeadlineExceeded):
		return contract.PipeError{
			Code:      CodeUnavailable,
			Message:   "the request timed out before the core answered",
			Retryable: true,
		}
	default:
		return contract.PipeError{Code: CodeInternal, Message: err.Error(), Retryable: false}
	}
}

// peekRequestIdentity reads the envelope fields that may be present in a frame
// that failed validation. It never fails: missing fields stay empty.
func peekRequestIdentity(frame []byte) (requestID, operation string) {
	var partial struct {
		RequestID string `json:"request_id"`
		Operation string `json:"operation"`
	}
	_ = json.Unmarshal(frame, &partial)
	return partial.RequestID, partial.Operation
}
