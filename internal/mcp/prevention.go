package mcp

import (
	"context"
	"encoding/json"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// preventionRegisterArguments deliberately excludes source, actor, repository
// paths, commands, file bodies and status. The caller supplies only metadata
// and a hash it has already calculated; MCP never reads or executes artifacts.
type preventionRegisterArguments struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	ArtifactPath  string `json:"artifact_path"`
	ContentSHA256 string `json:"content_sha256"`
}

func preventionRegisterTool(backend Backend) Tool {
	return Tool{
		Name: ToolPreventionRegister,
		Description: "Register a prevention draft using repository-relative artifact metadata. " +
			"The caller must calculate content_sha256; MCP does not read the artifact or verify its hash. " +
			"Only the user can mechanically verify or activate it through the CLI. " + CorrectionNotice,
		ReadOnly: false,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"issue_id":       map[string]any{"type": "string", "description": "Issue id associated with the prevention."},
			"kind":           map[string]any{"type": "string", "enum": []string{"regression_test", "deterministic_check", "agent_instruction", "intent"}},
			"title":          map[string]any{"type": "string", "description": "Short redacted prevention title."},
			"artifact_path":  map[string]any{"type": "string", "description": "Repository-relative slash path; never an absolute path."},
			"content_sha256": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "SHA-256 calculated by the caller; not mechanically verified by MCP."},
		}, []string{"issue_id", "kind", "title", "artifact_path", "content_sha256"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var input preventionRegisterArguments
			if err := decodeArguments(arguments, &input); err != nil {
				return nil, err
			}
			request := contract.PreventionAddRequest{
				SchemaVersion: input.SchemaVersion,
				IssueID:       input.IssueID,
				Kind:          input.Kind,
				Title:         input.Title,
				ArtifactPath:  input.ArtifactPath,
				ContentSHA256: input.ContentSHA256,
				Source:        "mcp",
				Actor:         "mcp",
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("prevention_register", err)
			}
			response, err := backend.PreventionAdd(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("prevention_register", err)
			}
			return response, nil
		},
	}
}

func preventionListTool(backend Backend) Tool {
	return Tool{
		Name: ToolPreventionList,
		Description: "List prevention artifacts and versions by issue, project, kind or status. " +
			"This is read-only; verification and activation remain user-only CLI operations. " + CorrectionNotice,
		ReadOnly: true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"issue_id":       map[string]any{"type": "string"},
			"project_key":    map[string]any{"type": "string"},
			"kind":           map[string]any{"type": "string", "enum": []string{"regression_test", "deterministic_check", "agent_instruction", "intent"}},
			"status":         map[string]any{"type": "string", "enum": []string{"draft", "verified", "active", "deprecated"}},
			"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"cursor":         map[string]any{"type": "string"},
		}, nil),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.PreventionListRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("prevention_list", err)
			}
			response, err := backend.PreventionList(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("prevention_list", err)
			}
			return response, nil
		},
	}
}

func preventionGetTool(backend Backend) Tool {
	return Tool{
		Name: ToolPreventionGet,
		Description: "Read one prevention artifact, its versions and recorded verification evidence by artifact id. " +
			"This does not run verification or activation; those remain user-only CLI operations. " + CorrectionNotice,
		ReadOnly: true,
		InputSchema: objectSchema(map[string]any{
			"schema_version": schemaVersion(),
			"artifact_id":    map[string]any{"type": "string", "description": "Prevention artifact id."},
		}, []string{"artifact_id"}),
		call: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var request contract.PreventionGetRequest
			if err := decodeArguments(arguments, &request); err != nil {
				return nil, err
			}
			fillVersion(&request.SchemaVersion)
			if err := request.Validate(); err != nil {
				return nil, invalidArguments("prevention_get", err)
			}
			response, err := backend.PreventionGet(ctx, request)
			if err != nil {
				return nil, err
			}
			if err := response.Validate(); err != nil {
				return nil, responseDrift("prevention_get", err)
			}
			return response, nil
		},
	}
}
