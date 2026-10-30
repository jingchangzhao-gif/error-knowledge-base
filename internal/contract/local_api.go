// The local Named Pipe envelope (EKB-008). Payloads are the MCP types from
// tools.go: the dispatcher below decodes each operation into the same struct
// and calls the same Validate method, so the two channels cannot drift.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// PipeEnvelopeVersion versions the envelope itself. Every payload still
// carries its own operation schema_version (ToolSchemaVersion for now).
const PipeEnvelopeVersion = "v1"

var validOperations = map[string]bool{
	OperationLookup:             true,
	OperationSearch:             true,
	OperationRecord:             true,
	OperationFeedback:           true,
	OperationList:               true,
	OperationStats:              true,
	OperationCapture:            true,
	OperationRecordSolution:     true,
	OperationSetStatus:          true,
	OperationGetIssue:           true,
	OperationSetSolutionStatus:  true,
	OperationDeleteSolution:     true,
	OperationPreventionAdd:      true,
	OperationPreventionVerify:   true,
	OperationPreventionActivate: true,
	OperationPreventionList:     true,
	OperationPreventionGet:      true,
	OperationPreventionStats:    true,
}

var validErrorCodes = map[string]bool{
	"invalid_request": true,
	"not_found":       true,
	"unavailable":     true,
	"conflict":        true,
	"internal":        true,
}

// PipeRequest is one newline-delimited JSON request on the local pipe.
type PipeRequest struct {
	SchemaVersion string          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Payload       json.RawMessage `json:"payload"`
}

// PipeResponse answers a PipeRequest. Exactly one of Result and Error is
// present; DecodePipeResponse enforces that.
type PipeResponse struct {
	SchemaVersion string          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *PipeError      `json:"error,omitempty"`
}

// PipeError is the error branch. A retryable error is the only kind a caller
// may replay automatically.
type PipeError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Validate checks the closed error code set and the message.
func (e PipeError) Validate() error {
	if !validErrorCodes[e.Code] {
		return fmt.Errorf("pipe error.code: unknown code %q", e.Code)
	}
	if e.Message == "" {
		return errors.New("pipe error.message: required")
	}
	return nil
}

// DecodePipeRequest parses one request and validates the envelope and the
// operation payload. It is the single entry point the daemon uses.
func DecodePipeRequest(data []byte) (PipeRequest, error) {
	var request PipeRequest
	if err := decodeStrict(data, &request); err != nil {
		return PipeRequest{}, fmt.Errorf("decode pipe request: %w", err)
	}
	if err := request.validate(); err != nil {
		return PipeRequest{}, err
	}
	return request, nil
}

func (r PipeRequest) validate() error {
	if r.SchemaVersion != PipeEnvelopeVersion {
		return fmt.Errorf("pipe request.schema_version: want %q, got %q", PipeEnvelopeVersion, r.SchemaVersion)
	}
	if r.RequestID == "" {
		return errors.New("pipe request.request_id: required")
	}
	if !validOperations[r.Operation] {
		return fmt.Errorf("pipe request.operation: unknown operation %q", r.Operation)
	}
	if len(r.Payload) == 0 {
		return errors.New("pipe request.payload: required")
	}
	return validatePayload(r.Operation, r.Payload)
}

// DecodePipeResponse parses one response, enforces "exactly one of result and
// error" and validates whichever branch is present.
func DecodePipeResponse(data []byte) (PipeResponse, error) {
	var response PipeResponse
	if err := decodeStrict(data, &response); err != nil {
		return PipeResponse{}, fmt.Errorf("decode pipe response: %w", err)
	}
	if response.SchemaVersion != PipeEnvelopeVersion {
		return PipeResponse{}, fmt.Errorf("pipe response.schema_version: want %q, got %q", PipeEnvelopeVersion, response.SchemaVersion)
	}
	if response.RequestID == "" {
		return PipeResponse{}, errors.New("pipe response.request_id: required")
	}
	if !validOperations[response.Operation] {
		return PipeResponse{}, fmt.Errorf("pipe response.operation: unknown operation %q", response.Operation)
	}
	hasResult := len(response.Result) > 0
	hasError := response.Error != nil
	switch {
	case hasResult && hasError:
		return PipeResponse{}, errors.New("pipe response: result and error are mutually exclusive")
	case !hasResult && !hasError:
		return PipeResponse{}, errors.New("pipe response: exactly one of result and error is required")
	}
	if hasError {
		if err := response.Error.Validate(); err != nil {
			return PipeResponse{}, err
		}
		return response, nil
	}
	if err := validateResult(response.Operation, response.Result); err != nil {
		return PipeResponse{}, err
	}
	return response, nil
}

// validatePayload dispatches on the operation and reuses the MCP Validate
// method. Adding an operation means adding both a case here and a tool in
// spec/mcp-tools.cue; the two lists are checked by the card's mapping table.
func validatePayload(operation string, payload []byte) error {
	switch operation {
	case OperationLookup:
		var decoded LookupRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("lookup payload: %w", err)
		}
		return decoded.Validate()
	case OperationSearch:
		var decoded SearchRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("search payload: %w", err)
		}
		return decoded.Validate()
	case OperationRecord:
		var decoded RecordRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("record payload: %w", err)
		}
		return decoded.Validate()
	case OperationFeedback:
		var decoded FeedbackRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("feedback payload: %w", err)
		}
		return decoded.Validate()
	case OperationList:
		var decoded ListRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("list payload: %w", err)
		}
		return decoded.Validate()
	case OperationStats:
		var decoded StatsRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("stats payload: %w", err)
		}
		return decoded.Validate()
	case OperationCapture:
		var decoded CaptureRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("capture payload: %w", err)
		}
		return decoded.Validate()
	case OperationRecordSolution:
		var decoded RecordSolutionRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("record_solution payload: %w", err)
		}
		return decoded.Validate()
	case OperationSetStatus:
		var decoded SetStatusRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("set_status payload: %w", err)
		}
		return decoded.Validate()
	case OperationGetIssue:
		var decoded GetIssueRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("get_issue payload: %w", err)
		}
		return decoded.Validate()
	case OperationSetSolutionStatus:
		var decoded SetSolutionStatusRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("set_solution_status payload: %w", err)
		}
		return decoded.Validate()
	case OperationDeleteSolution:
		var decoded DeleteSolutionRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("delete_solution payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionAdd:
		var decoded PreventionAddRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_add payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionVerify:
		var decoded PreventionVerifyRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_verify payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionActivate:
		var decoded PreventionActivateRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_activate payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionList:
		var decoded PreventionListRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_list payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionGet:
		var decoded PreventionGetRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_get payload: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionStats:
		var decoded PreventionStatsRequest
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_stats payload: %w", err)
		}
		return decoded.Validate()
	default:
		return fmt.Errorf("operation: unknown %q", operation)
	}
}

// validateResult mirrors validatePayload for the response branch, so a daemon
// cannot answer with a payload the MCP path would reject.
func validateResult(operation string, payload []byte) error {
	switch operation {
	case OperationLookup:
		var decoded LookupResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("lookup result: %w", err)
		}
		return decoded.Validate()
	case OperationSearch:
		var decoded SearchResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("search result: %w", err)
		}
		return decoded.Validate()
	case OperationRecord:
		var decoded RecordResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("record result: %w", err)
		}
		return decoded.Validate()
	case OperationFeedback:
		var decoded FeedbackResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("feedback result: %w", err)
		}
		return decoded.Validate()
	case OperationList:
		var decoded ListResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("list result: %w", err)
		}
		return decoded.Validate()
	case OperationStats:
		var decoded StatsResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("stats result: %w", err)
		}
		return decoded.Validate()
	case OperationCapture:
		var decoded CaptureResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("capture result: %w", err)
		}
		return decoded.Validate()
	case OperationRecordSolution:
		var decoded RecordSolutionResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("record_solution result: %w", err)
		}
		return decoded.Validate()
	case OperationSetStatus:
		var decoded SetStatusResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("set_status result: %w", err)
		}
		return decoded.Validate()
	case OperationGetIssue:
		var decoded GetIssueResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("get_issue result: %w", err)
		}
		return decoded.Validate()
	case OperationSetSolutionStatus:
		var decoded SetSolutionStatusResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("set_solution_status result: %w", err)
		}
		return decoded.Validate()
	case OperationDeleteSolution:
		var decoded DeleteSolutionResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("delete_solution result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionAdd:
		var decoded PreventionAddResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_add result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionVerify:
		var decoded PreventionVerifyResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_verify result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionActivate:
		var decoded PreventionActivateResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_activate result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionList:
		var decoded PreventionListResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_list result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionGet:
		var decoded PreventionGetResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_get result: %w", err)
		}
		return decoded.Validate()
	case OperationPreventionStats:
		var decoded PreventionStatsResponse
		if err := decodeStrict(payload, &decoded); err != nil {
			return fmt.Errorf("prevention_stats result: %w", err)
		}
		return decoded.Validate()
	default:
		return fmt.Errorf("operation: unknown %q", operation)
	}
}

// decodeStrict rejects unknown fields and trailing data, the same rules
// DecodeErrorEvent applies to an event.
func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}
