// The six MCP tools (EKB-037). Each tool is the same operation the local Named
// Pipe API and the CLI speak, wrapped in one MCP tool envelope: the server does
// not get its own retrieval or write path, and the response is re-validated
// against the contract before it leaves.
//
// MCP is the active query and record channel. It is **not** passive capture: no
// tool watches a host, subscribes to events or reports failures on its own.
// Passive capture belongs to hooks, plugins, CLI wrappers (errkb run) and log
// channels (plan section 3.1); a host that installs this server gets exactly
// the six calls below.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// The twelve tool names are the complete MCP operation surface.
const (
	ToolLookup             = "err_lookup"
	ToolSearch             = "err_search"
	ToolRecord             = "err_record"
	ToolFeedback           = "err_feedback"
	ToolList               = "err_list"
	ToolStats              = "err_stats"
	ToolRecordSolution     = "err_record_solution"
	ToolSetStatus          = "err_set_status"
	ToolGetIssue           = "err_get_issue"
	ToolPreventionRegister = "err_prevention_register"
	ToolPreventionList     = "err_prevention_list"
	ToolPreventionGet      = "err_prevention_get"
)

// CorrectionNotice is published in initialize.instructions and appended to
// every tool description. MCP can register prevention drafts and read their
// state, but only a human invoking the CLI can mechanically verify or activate.
const CorrectionNotice = "MCP provides active lookup, search, record, feedback, list, stats, solution recording, status correction, prevention draft registration and prevention queries only. " +
	"It is not passive capture: no tool watches a host, subscribes to events or reports failures by itself. " +
	"Passive capture belongs to hooks, plugins, CLI wrappers and log channels. " +
	"Prevention verify and activate are available only to the user through the CLI; MCP never performs those transitions."

// ErrInvalidArguments marks a tool argument that breaks the contract. The
// server maps it to JSON-RPC invalid-params (-32602); backend failures map to
// an internal error instead, so a caller can tell "you asked wrong" from
// "the daemon is down".
var ErrInvalidArguments = errors.New("invalid tool arguments")

// Backend is the operation surface tools reuse. The daemon's SQL backend and
// the CLI's pipe client both speak it; a tool never talks to PostgreSQL. It
// deliberately has no PreventionVerify or PreventionActivate method.
type Backend interface {
	Lookup(context.Context, contract.LookupRequest) (contract.LookupResponse, error)
	Search(context.Context, contract.SearchRequest) (contract.SearchResponse, error)
	Record(context.Context, contract.RecordRequest) (contract.RecordResponse, error)
	Feedback(context.Context, contract.FeedbackRequest) (contract.FeedbackResponse, error)
	List(context.Context, contract.ListRequest) (contract.ListResponse, error)
	Stats(context.Context, contract.StatsRequest) (contract.StatsResponse, error)
	RecordSolution(context.Context, contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error)
	SetStatus(context.Context, contract.SetStatusRequest) (contract.SetStatusResponse, error)
	GetIssue(context.Context, contract.GetIssueRequest) (contract.GetIssueResponse, error)
	PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error)
	PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error)
	PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error)
}

// Tool is one published descriptor plus its typed call.
type Tool struct {
	Name        string
	Description string
	ReadOnly    bool
	InputSchema map[string]any
	call        func(context.Context, json.RawMessage) (any, error)
}

// Call validates and normalises the arguments, runs the backend call and
// re-validates the response. All argument errors wrap ErrInvalidArguments.
func (t Tool) Call(ctx context.Context, arguments json.RawMessage) (any, error) {
	if t.call == nil {
		return nil, fmt.Errorf("tool %s: no implementation", t.Name)
	}
	return t.call(ctx, arguments)
}

// NewTools builds the twelve tools over one backend. Prevention registration
// only creates drafts; verification and activation remain human CLI actions.
func NewTools(backend Backend) []Tool {
	return []Tool{
		lookupTool(backend),
		searchTool(backend),
		recordTool(backend),
		feedbackTool(backend),
		listTool(backend),
		statsTool(backend),
		recordSolutionTool(backend),
		setStatusTool(backend),
		getIssueTool(backend),
		preventionRegisterTool(backend),
		preventionListTool(backend),
		preventionGetTool(backend),
	}
}

func lookupTool(backend Backend) Tool {
	return Tool{
		Name:        ToolLookup,
		Description: "Find known issues by issue id, error code or exact fingerprint. Returns at most three candidates with the match reason. " + CorrectionNotice,
		ReadOnly:    true,
		InputSchema: objectSchema(map[string]any{
			"schema_version":     schemaVersion(),
			"issue":              map[string]any{"type": "string", "description": "Issue id to look up."},
			"error_code":         map[string]any{"type": "string", "description": "Machine error code, for example EPERM."},
			"fingerprint":        map[string]any{"type": "string", "pattern": "^[0-9a-f]{12}$"},
			"normalized_message": map[string]any{"type": "string", "description": "Normalized message text (redacted)."},
			"project_key":        map[string]any{"type": "string"},
		}, nil),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.LookupRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("lookup", err)
			}
			response, err := backend.Lookup(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("lookup", err)
			}
			return response, nil
		},
	}
}

func searchTool(backend Backend) Tool {
	return Tool{
		Name:        ToolSearch,
		Description: "Search known issues by free text. Returns at most three candidates with the match reason and whether each is approximate. " + CorrectionNotice,
		ReadOnly:    true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"query":          map[string]any{"type": "string", "description": "Free-text description of the failure (redacted)."},
			"project_key":    map[string]any{"type": "string"},
			"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": contract.MaxCandidates},
		}, []string{"query"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.SearchRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("search", err)
			}
			response, err := backend.Search(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("search", err)
			}
			return response, nil
		},
	}
}

func recordTool(backend Backend) Tool {
	eventProperties := map[string]any{
		"schema_version":     schemaVersion(),
		"event_id":           map[string]any{"type": "string"},
		"connector_id":       map[string]any{"type": "string"},
		"session_id":         map[string]any{"type": "string"},
		"observed_at":        map[string]any{"type": "string", "description": "RFC 3339 timestamp."},
		"channel":            map[string]any{"type": "string", "enum": []string{"A", "B", "C", "D", "E"}},
		"category":           map[string]any{"type": "string", "enum": []string{"tool", "command-exit", "llm", "agent"}},
		"severity":           map[string]any{"type": "string"},
		"transient":          map[string]any{"type": "boolean"},
		"headline":           map[string]any{"type": "string", "description": "One redacted line."},
		"normalized_message": map[string]any{"type": "string"},
		"redacted_sample":    map[string]any{"type": "string"},
		"error_code":         map[string]any{"type": "string"},
		"command":            map[string]any{"type": "string"},
		"exit_code":          map[string]any{"type": "integer"},
		"tool_name":          map[string]any{"type": "string"},
		"os":                 map[string]any{"type": "string"},
		"shell":              map[string]any{"type": "string"},
		"runtime_versions":   map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		"project_key":        map[string]any{"type": "string"},
		"fingerprint":        map[string]any{"type": "string", "pattern": "^[0-9a-f]{12}$"},
		"evidence":           map[string]any{"type": "string"},
		"confidence":         map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	}
	return Tool{
		Name:        ToolRecord,
		Description: "Record one redacted ErrorEvent v1 and attach it to an issue. Records are explicit: the host submits the event, nothing is captured behind its back. " + CorrectionNotice,
		ReadOnly:    false,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"event": map[string]any{
				"type":                 "object",
				"description":          "One already-redacted ErrorEvent v1 (spec/error-event.cue).",
				"properties":           eventProperties,
				"required":             []string{"schema_version", "event_id", "connector_id", "session_id", "observed_at", "channel", "category", "severity", "transient", "headline", "normalized_message", "redacted_sample", "os", "project_key", "fingerprint", "evidence", "confidence"},
				"additionalProperties": false,
			},
		}, []string{"event"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.RecordRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("record", err)
			}
			response, err := backend.Record(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("record", err)
			}
			return response, nil
		},
	}
}

func feedbackTool(backend Backend) Tool {
	return Tool{
		Name:        ToolFeedback,
		Description: "Report whether a solution worked in this environment. It records an outcome; it does not set trust. " + CorrectionNotice,
		ReadOnly:    false,
		InputSchema: objectSchema(map[string]any{
			"schema_version":      schemaVersion(),
			"issue_id":            map[string]any{"type": "string"},
			"solution_version_id": map[string]any{"type": "string"},
			"outcome":             map[string]any{"type": "string", "enum": []string{"success", "failure", "unknown"}},
			"note":                map[string]any{"type": "string", "description": "Optional redacted note."},
		}, []string{"issue_id", "solution_version_id", "outcome"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.FeedbackRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("feedback", err)
			}
			response, err := backend.Feedback(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("feedback", err)
			}
			return response, nil
		},
	}
}

func listTool(backend Backend) Tool {
	return Tool{
		Name:        ToolList,
		Description: "List known issues or solutions with pagination. " + CorrectionNotice,
		ReadOnly:    true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"kind":           map[string]any{"type": "string", "enum": []string{"issue", "solution"}},
			"project_key":    map[string]any{"type": "string"},
			"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"cursor":         map[string]any{"type": "string"},
			"environment": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"os":               map[string]any{"type": "string"},
					"shell":            map[string]any{"type": "string"},
					"connector":        map[string]any{"type": "string"},
					"runtime_versions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				},
			},
		}, []string{"kind"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.ListRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("list", err)
			}
			response, err := backend.List(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("list", err)
			}
			return response, nil
		},
	}
}

func statsTool(backend Backend) Tool {
	return Tool{
		Name:        ToolStats,
		Description: "Report knowledge-base totals, retrieval latency and token savings, keeping estimated and measured savings separate. " + CorrectionNotice,
		ReadOnly:    true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"project_key":    map[string]any{"type": "string"},
		}, nil),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.StatsRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("stats", err)
			}
			response, err := backend.Stats(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("stats", err)
			}
			return response, nil
		},
	}
}

func recordSolutionTool(backend Backend) Tool {
	return Tool{
		Name: ToolRecordSolution,
		Description: "Record a verified fix against a known issue: appends the next solution version, " +
			"verifies the solution and resolves the issue. Trust still comes from recorded outcomes. " + CorrectionNotice,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"issue_id":       map[string]any{"type": "string", "description": "Issue id the fix belongs to."},
			"body":           map[string]any{"type": "string", "description": "The fix text, already redacted."},
			"preconditions":  map[string]any{"type": "string", "description": "When the fix applies, already redacted."},
			"environment": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"os":               map[string]any{"type": "string"},
					"shell":            map[string]any{"type": "string"},
					"connector":        map[string]any{"type": "string"},
					"runtime_versions": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				},
				"additionalProperties": false,
			},
			"source": map[string]any{"type": "string"},
			"actor":  map[string]any{"type": "string"},
		}, []string{"issue_id", "body"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.RecordSolutionRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("record_solution", err)
			}
			response, err := backend.RecordSolution(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("record_solution", err)
			}
			return response, nil
		},
	}
}

func setStatusTool(backend Backend) Tool {
	return Tool{
		Name: ToolSetStatus,
		Description: "Correct an issue status (open, resolved, wontfix or misjudged), for example when a " +
			"recorded failure was not the problem after all. " + CorrectionNotice,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"issue_id":       map[string]any{"type": "string"},
			"status":         map[string]any{"type": "string", "enum": []string{"open", "resolved", "wontfix", "misjudged"}},
		}, []string{"issue_id", "status"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.SetStatusRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("set_status", err)
			}
			response, err := backend.SetStatus(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("set_status", err)
			}
			return response, nil
		},
	}
}

func getIssueTool(backend Backend) Tool {
	return Tool{
		Name: ToolGetIssue,
		Description: "Read one issue with its occurrences, solution versions and outcome history, " +
			"including the recorded fix text and the current trust score. " + CorrectionNotice,
		ReadOnly: true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"issue_id":       map[string]any{"type": "string", "description": "Issue id (UUID)."},
		}, []string{"issue_id"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.GetIssueRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("get_issue", err)
			}
			response, err := backend.GetIssue(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("get_issue", err)
			}
			return response, nil
		},
	}
}

// decodeArguments decodes one tools/call argument object. Unknown fields are
// rejected so a client typo fails loudly instead of being silently dropped;
// an absent object means "no arguments" and validates like an empty struct.
func decodeArguments(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidArguments("arguments", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return invalidArguments("arguments", errors.New("unexpected trailing data"))
	}
	return nil
}

func fillVersion(version *string) {
	if *version == "" {
		*version = contract.ToolSchemaVersion
	}
}

func invalidArguments(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidArguments, operation, err)
}

func responseDrift(operation string, err error) error {
	return fmt.Errorf("the daemon returned a %s response that breaks the tool contract: %w", operation, err)
}

func schemaVersion() map[string]any {
	return map[string]any{"type": "string", "description": "Tool contract version; defaults to " + contract.ToolSchemaVersion + "."}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
