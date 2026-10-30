// Schema compatibility rules for the five errkb contracts.
//
// This file is executable, not documentation. The `compat` job in
// .github/workflows/ci.yml exports it from the merge base and from HEAD,
// classifies the difference with the rule table below, and fails the build
// when a breaking or needs-version-bump change lands without a schema_version
// increase. The same job first runs the checker's self-test, so a rule that
// stops firing is itself a failure.
//
// The three change classes
//   compatible          - an added optional field or a new enum value; both
//                         directions keep old writers valid, so no version bump.
//   breaking            - removing or renaming a field, changing a field
//                         between required and optional, shrinking an enum,
//                         changing the unknown-field policy, moving a contract.
//                         Requires a schema_version increase (rule R9) and a
//                         migration or read path on the consuming side.
//   needs-version-bump  - the shape is unchanged but the meaning changed
//                         (semantics text, decoder binding, version anchor).
//                         Same bytes, different interpretation: bump first.
//
// Unknown-field policy
//   Every v1 decoder is strict: internal/contract/error_event.go and
//   internal/contract/local_api.go call DisallowUnknownFields, and
//   internal/contract/config.go calls KnownFields(true). The declared policy
//   below therefore matches what the code does. Persisted ErrorEvent rows are
//   read back through version-specific readers during database upgrades
//   (EKB-053); that tolerant read path is deliberately not declared here,
//   because it is not the v1 wire decoder.
//
// Version anchors
//   error_event           #SchemaVersion in spec/error-event.cue
//   local_api             #EnvelopeVersion in spec/local-api.cue
//   mcp_tools             schema_version on every request and response payload
//   config                no on-wire version; this manifest owns its version
//   connector_capability  no on-wire version; this manifest owns its version
//
// How to change a contract
//   1. Edit the CUE file and, in the same commit, the matching entry in
//      `contracts` (fields, required, enums). The R12 check fails on drift.
//   2. Classify the change against the rules. A breaking or meaning change
//      must also raise schema_version from vN to a strictly greater vN.
//   3. The compatibility job proves the classification on the real diff.
//
// Contracts are declared per file; every spec/*.cue except this file must
// appear here (R14), so no schema can change outside the review.
package spec

compat: {
	rules: [
		{id: "R1", change_class: "compatible", verdict: "allow", why: "An added optional field keeps every existing writer valid; old readers ignore it and new readers may use it."},
		{id: "R2", change_class: "breaking", verdict: "fail", why: "A new required field invalidates every existing producer, so the version must move and a read path for older writers must exist."},
		{id: "R3", change_class: "breaking", verdict: "fail", why: "Removing a field destroys information that old producers still send and old rows still store."},
		{id: "R4", change_class: "breaking", verdict: "fail", why: "Changing a field between required and optional changes what is valid; either direction breaks one side."},
		{id: "R5", change_class: "compatible", verdict: "allow", why: "A larger enum rejects fewer values; no existing writer becomes invalid."},
		{id: "R6", change_class: "breaking", verdict: "fail", why: "A smaller enum, or a removed enum set, rejects values that used to be valid."},
		{id: "R7", change_class: "needs-version-bump", verdict: "fail without a version bump", why: "Meaning changed while the shape stayed: the same bytes are read differently, so the version must move even though the diff looks harmless."},
		{id: "R8", change_class: "breaking", verdict: "fail", why: "The unknown-field policy is part of the wire contract; changing it changes which payloads are accepted."},
		{id: "R9", change_class: "breaking", verdict: "fail", why: "Any breaking or needs-version-bump change must raise schema_version from vN to a strictly greater vN."},
		{id: "R10", change_class: "compatible", verdict: "allow", why: "A new contract has no old producers to break."},
		{id: "R11", change_class: "static", verdict: "fail", why: "Removing a contract strands every client that still speaks it; this needs an owner decision, not a silent schema gate."},
		{id: "R12", change_class: "static", verdict: "fail", why: "The manifest is the baseline: if it disagrees with the CUE definitions, later diffs would measure the wrong surface."},
		{id: "R13", change_class: "static", verdict: "fail", why: "A declared unknown-field policy that its decoder does not implement is a false statement in the contract."},
		{id: "R14", change_class: "static", verdict: "fail", why: "A spec file outside the manifest could change without any compatibility review."},
		{id: "R15", change_class: "static", verdict: "fail", why: "An unexercised rule is a rule that can rot; every declared rule must have a self-test case that fires."},
		{id: "R16", change_class: "breaking", verdict: "fail", why: "Moving or renaming a contract changes where clients find it, so it is a breaking change."},
		{id: "R17", change_class: "compatible", verdict: "allow", why: "Adding a definition that only optional fields reference (for example a nested solution object) accepts more payloads and invalidates no existing writer."},
		{id: "R18", change_class: "compatible", verdict: "allow", why: "An enum set introduced by a new definition is reachable only through that definition, so it adds accepted payloads without invalidating an existing writer."},
		{id: "R19", change_class: "compatible", verdict: "allow", why: "A required field added underneath a brand-new optional parent (for example config.templates.log.rotation) is reachable only through that new parent, so no existing document becomes invalid."},
	]
	contracts: {
		error_event: {
			file: "spec/error-event.cue"
			definitions: ["ErrorEvent"]
			schema_version: "v1"
			semantics:      "One redacted ErrorEvent v1 observation: identity, capture context, sanitized text and the fingerprint. Issues, solutions and hit counts are separate entities and never appear on the event."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/error_event.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#SchemaVersion"
			fields: [
				"schema_version",
				"event_id",
				"connector_id",
				"session_id",
				"observed_at",
				"channel",
				"category",
				"severity",
				"transient",
				"headline",
				"normalized_message",
				"redacted_sample",
				"error_code",
				"command",
				"exit_code",
				"tool_name",
				"os",
				"shell",
				"runtime_versions",
				"project_key",
				"fingerprint",
				"evidence",
				"confidence",
			]
			required: [
				"schema_version",
				"event_id",
				"connector_id",
				"session_id",
				"observed_at",
				"channel",
				"category",
				"severity",
				"transient",
				"headline",
				"normalized_message",
				"redacted_sample",
				"os",
				"project_key",
				"fingerprint",
				"evidence",
				"confidence",
			]
			enums: {
				channel: ["A", "B", "C", "D", "E"]
				category: ["tool", "command-exit", "llm", "agent"]
			}
		}
		config: {
			file: "spec/config.cue"
			definitions: ["Config"]
			schema_version: "v1"
			semantics:      "User-editable daemon configuration, written as YAML or JSON: database connection, local service channels, capture pause filters, notice policy, retention periods, redaction extensions, search limit, timeouts and the offline spool. It has no on-wire version field; this manifest owns its version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/config.go"
				marker: "KnownFields(true)"
			}
			fields: [
				"database",
				"database.dsn",
				"database.max_connections",
				"database.connect_timeout_ms",
				"service",
				"service.pipe",
				"service.pipe.name",
				"service.pipe.enabled",
				"service.http",
				"service.http.enabled",
				"service.http.host",
				"service.http.port",
				"service.http.token",
				"capture",
				"capture.enabled",
				"capture.paused_connectors",
				"capture.paused_projects",
				"notices",
				"notices.before_diagnosis",
				"retention",
				"retention.sample_days",
				"retention.normalized_days",
				"retention.audit_days",
				"redaction",
				"redaction.extra_patterns",
				"search",
				"search.limit",
				"timeout",
				"timeout.capture_ms",
				"timeout.search_ms",
				"timeout.mcp_ms",
				"offline",
				"offline.spool_dir",
				"offline.replay_interval_ms",
				"templates",
				"templates.cli",
				"templates.cli.enabled",
				"templates.cli.command",
				"templates.cli.capture_exit_codes",
				"templates.log",
				"templates.log.enabled",
				"templates.log.path",
				"templates.log.encoding",
				"templates.log.rotation",
				"templates.log.rotation.max_bytes",
				"templates.log.rotation.keep",
				"templates.manual",
				"templates.manual.enabled",
				"templates.manual.note",
			]
			required: [
				"database",
				"database.dsn",
				"service",
				"service.pipe",
				"service.pipe.enabled",
				"service.http",
				"service.http.enabled",
				"capture",
				"capture.enabled",
				"notices",
				"notices.before_diagnosis",
				"retention",
				"retention.sample_days",
				"retention.normalized_days",
				"retention.audit_days",
				"redaction",
				"search",
				"search.limit",
				"timeout",
				"timeout.capture_ms",
				"timeout.search_ms",
				"timeout.mcp_ms",
				"offline",
				"offline.replay_interval_ms",
				"templates.cli.enabled",
				"templates.cli.command",
				"templates.cli.capture_exit_codes",
				"templates.log.enabled",
				"templates.log.path",
				"templates.log.encoding",
				"templates.log.rotation.max_bytes",
				"templates.log.rotation.keep",
				"templates.manual.enabled",
			]
		}
		connector: {
			file: "spec/connector.cue"
			definitions: ["Observation", "ConnectorCaptureRequest", "ConnectorCaptureResponse", "ConnectorLookupRequest", "ConnectorLookupResponse", "ConnectorNoticeRequest", "ConnectorNoticeResponse", "ConnectorFeedbackRequest", "ConnectorFeedbackResponse"]
			schema_version: "v1"
			semantics:      "The four connector SDK actions (capture, lookup, notice, feedback) a host adapter may take against the core: versioned envelopes, a raw observation before the core classifies and redacts it, at most three reason-carrying candidates, explicit degraded flags and an outcome that never computes trust. The capability declaration and its probe result stay #ConnectorCapability in connector-capability.cue and are not repeated here."
			unknown_fields: "tolerate"
			decoder: {
				file:   "internal/capture/connector.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#ConnectorSDKVersion"
			fields: [
				"ConnectorCaptureRequest.channel",
				"ConnectorCaptureRequest.connector_id",
				"ConnectorCaptureRequest.observation",
				"ConnectorCaptureRequest.observation_id",
				"ConnectorCaptureRequest.schema_version",
				"ConnectorCaptureRequest.session_id",
				"ConnectorCaptureResponse.created",
				"ConnectorCaptureResponse.event_id",
				"ConnectorCaptureResponse.fingerprint",
				"ConnectorCaptureResponse.issue_id",
				"ConnectorCaptureResponse.occurrence_id",
				"ConnectorCaptureResponse.schema_version",
				"ConnectorFeedbackRequest.connector_id",
				"ConnectorFeedbackRequest.environment",
				"ConnectorFeedbackRequest.environment.connector",
				"ConnectorFeedbackRequest.environment.os",
				"ConnectorFeedbackRequest.environment.runtime_versions",
				"ConnectorFeedbackRequest.environment.shell",
				"ConnectorFeedbackRequest.issue_id",
				"ConnectorFeedbackRequest.note",
				"ConnectorFeedbackRequest.outcome",
				"ConnectorFeedbackRequest.schema_version",
				"ConnectorFeedbackRequest.session_id",
				"ConnectorFeedbackRequest.solution_version_id",
				"ConnectorFeedbackResponse.outcome_id",
				"ConnectorFeedbackResponse.recorded",
				"ConnectorFeedbackResponse.schema_version",
				"ConnectorLookupRequest.connector_id",
				"ConnectorLookupRequest.error_code",
				"ConnectorLookupRequest.fingerprint",
				"ConnectorLookupRequest.issue_id",
				"ConnectorLookupRequest.normalized_message",
				"ConnectorLookupRequest.project_key",
				"ConnectorLookupRequest.schema_version",
				"ConnectorLookupRequest.session_id",
				"ConnectorLookupResponse.candidates",
				"ConnectorLookupResponse.degraded",
				"ConnectorLookupResponse.schema_version",
				"ConnectorNoticeRequest.candidate",
				"ConnectorNoticeRequest.connector_id",
				"ConnectorNoticeRequest.issue_id",
				"ConnectorNoticeRequest.kind",
				"ConnectorNoticeRequest.schema_version",
				"ConnectorNoticeRequest.session_id",
				"ConnectorNoticeRequest.trust",
				"ConnectorNoticeResponse.chars",
				"ConnectorNoticeResponse.kind",
				"ConnectorNoticeResponse.schema_version",
				"ConnectorNoticeResponse.text",
				"ConnectorNoticeResponse.tokens",
				"Observation.command",
				"Observation.confidence",
				"Observation.error_code",
				"Observation.evidence",
				"Observation.exit_code",
				"Observation.message",
				"Observation.observed_at",
				"Observation.os",
				"Observation.project_key",
				"Observation.runtime_versions",
				"Observation.shell",
				"Observation.source",
				"Observation.tool_name",
				"ConnectorCaptureResponse.queued",
			]
			required: [
				"ConnectorCaptureRequest.connector_id",
				"ConnectorCaptureRequest.observation",
				"ConnectorCaptureRequest.schema_version",
				"ConnectorCaptureRequest.session_id",
				"ConnectorCaptureResponse.created",
				"ConnectorCaptureResponse.event_id",
				"ConnectorCaptureResponse.fingerprint",
				"ConnectorCaptureResponse.issue_id",
				"ConnectorCaptureResponse.occurrence_id",
				"ConnectorCaptureResponse.schema_version",
				"ConnectorFeedbackRequest.connector_id",
				"ConnectorFeedbackRequest.issue_id",
				"ConnectorFeedbackRequest.outcome",
				"ConnectorFeedbackRequest.schema_version",
				"ConnectorFeedbackRequest.solution_version_id",
				"ConnectorFeedbackResponse.outcome_id",
				"ConnectorFeedbackResponse.recorded",
				"ConnectorFeedbackResponse.schema_version",
				"ConnectorLookupRequest.connector_id",
				"ConnectorLookupRequest.schema_version",
				"ConnectorLookupResponse.candidates",
				"ConnectorLookupResponse.degraded",
				"ConnectorLookupResponse.schema_version",
				"ConnectorNoticeRequest.connector_id",
				"ConnectorNoticeRequest.kind",
				"ConnectorNoticeRequest.schema_version",
				"ConnectorNoticeResponse.chars",
				"ConnectorNoticeResponse.kind",
				"ConnectorNoticeResponse.schema_version",
				"ConnectorNoticeResponse.text",
				"ConnectorNoticeResponse.tokens",
				"Observation.message",
				"Observation.observed_at",
				"Observation.source",
			]
			enums: {
				notice_kind: ["hit", "miss"]
				notice_trust: ["trusted", "doubted", "suppressed"]
				notice_result_kind: ["hit", "near", "no-fix", "miss", "doubted"]
			}
		}
		connector_capability: {
			file: "spec/connector-capability.cue"
			definitions: ["ConnectorCapability"]
			schema_version: "v1"
			semantics:      "What one host can capture at its reliability channel, whether it can receive a known fix before diagnosing, and the explicit fallback when the best channel is unavailable. It has no on-wire version field; this manifest owns its version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/config.go"
				marker: "KnownFields(true)"
			}
			fields: [
				"connector",
				"host_version",
				"channel",
				"captures",
				"notice_before_diagnosis",
				"fallback",
				"fallback.channel",
				"fallback.missing",
				"fallback.note",
				"probe",
				"probe.probed_at",
				"probe.host_version",
				"probe.ok",
				"probe.missing",
			]
			required: [
				"connector",
				"host_version",
				"channel",
				"captures",
				"notice_before_diagnosis",
				"fallback",
				"fallback.channel",
				"fallback.missing",
				"probe",
				"probe.probed_at",
				"probe.host_version",
				"probe.ok",
				"probe.missing",
			]
			enums: {
				channel: ["A", "B", "C", "D", "E"]
				event_field: ["schema_version", "event_id", "connector_id", "session_id", "observed_at", "channel", "category", "severity", "transient", "headline", "normalized_message", "redacted_sample", "error_code", "command", "exit_code", "tool_name", "os", "shell", "runtime_versions", "project_key", "fingerprint", "evidence", "confidence"]
			}
		}
		mcp_tools: {
			file: "spec/mcp-tools.cue"
			definitions: ["LookupRequest", "LookupResponse", "SearchRequest", "SearchResponse", "RecordRequest", "RecordResponse", "FeedbackRequest", "FeedbackResponse", "ListRequest", "ListResponse", "StatsRequest", "StatsResponse", "Candidate", "Solution", "ListItem", "Tool", "SolutionEnvironment", "RecordSolutionRequest", "RecordSolutionResponse", "SetStatusRequest", "SetStatusResponse"]
			schema_version: "v1"
			semantics:      "The six MCP operations (lookup, search, record, feedback, list, stats) with their request and response payloads, the reason-carrying candidate shape and the three-candidate ceiling. Each payload carries its own schema_version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/local_api.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "per_operation"
			fields: [
				"LookupRequest.schema_version",
				"LookupRequest.issue",
				"LookupRequest.error_code",
				"LookupRequest.fingerprint",
				"LookupRequest.normalized_message",
				"LookupRequest.project_key",
				"LookupResponse.schema_version",
				"LookupResponse.candidates",
				"SearchRequest.schema_version",
				"SearchRequest.query",
				"SearchRequest.project_key",
				"SearchRequest.limit",
				"SearchResponse.schema_version",
				"SearchResponse.candidates",
				"RecordRequest.schema_version",
				"RecordRequest.event",
				"RecordResponse.schema_version",
				"RecordResponse.event_id",
				"RecordResponse.occurrence_id",
				"RecordResponse.issue_id",
				"RecordResponse.fingerprint",
				"RecordResponse.created",
				"FeedbackRequest.schema_version",
				"FeedbackRequest.issue_id",
				"FeedbackRequest.solution_version_id",
				"FeedbackRequest.outcome",
				"FeedbackRequest.note",
				"FeedbackResponse.schema_version",
				"FeedbackResponse.outcome_id",
				"FeedbackResponse.recorded",
				"ListRequest.schema_version",
				"ListRequest.kind",
				"ListRequest.project_key",
				"ListRequest.limit",
				"ListRequest.cursor",
				"ListRequest.environment",
				"ListResponse.schema_version",
				"ListResponse.items",
				"ListResponse.next_cursor",
				"StatsRequest.schema_version",
				"StatsRequest.project_key",
				"StatsResponse.schema_version",
				"StatsResponse.totals",
				"StatsResponse.totals.events",
				"StatsResponse.totals.issues",
				"StatsResponse.totals.solutions",
				"StatsResponse.totals.outcomes",
				"StatsResponse.retrieval",
				"StatsResponse.retrieval.p50_ms",
				"StatsResponse.retrieval.p95_ms",
				"StatsResponse.tokens",
				"StatsResponse.tokens.estimated_saved",
				"StatsResponse.tokens.measured_saved",
				"StatsResponse.tokens.measured_events",
				"Candidate.issue_id",
				"Candidate.fingerprint",
				"Candidate.title",
				"Candidate.match_reason",
				"Candidate.match_detail",
				"Candidate.approximate",
				"Candidate.confidence",
				"Candidate.hits",
				"Candidate.solution",
				"Solution.solution_version_id",
				"Solution.body",
				"Solution.source",
				"Solution.applies_to",
				"Solution.trust",
				"ListItem.fix",
				"ListItem.hits",
				"ListItem.id",
				"ListItem.status",
				"ListItem.title",
				"ListItem.updated_at",
				"Tool.name",
				"Tool.description",
				"Tool.read_only",
				"Tool.input",
				"RecordSolutionRequest.actor",
				"RecordSolutionRequest.body",
				"RecordSolutionRequest.environment",
				"RecordSolutionRequest.issue_id",
				"RecordSolutionRequest.preconditions",
				"RecordSolutionRequest.schema_version",
				"RecordSolutionRequest.source",
				"RecordSolutionResponse.recorded",
				"RecordSolutionResponse.schema_version",
				"RecordSolutionResponse.solution_id",
				"RecordSolutionResponse.solution_version_id",
				"RecordSolutionResponse.version",
				"SetStatusRequest.issue_id",
				"SetStatusRequest.schema_version",
				"SetStatusRequest.status",
				"SetStatusResponse.issue_id",
				"SetStatusResponse.schema_version",
				"SetStatusResponse.status",
				"SolutionEnvironment.connector",
				"SolutionEnvironment.os",
				"SolutionEnvironment.runtime_versions",
				"SolutionEnvironment.shell",
				"Tool.output",
				"RecordResponse.queued",
			]
			required: [
				"LookupRequest.schema_version",
				"LookupResponse.schema_version",
				"LookupResponse.candidates",
				"SearchRequest.schema_version",
				"SearchRequest.query",
				"SearchResponse.schema_version",
				"SearchResponse.candidates",
				"RecordRequest.schema_version",
				"RecordRequest.event",
				"RecordResponse.schema_version",
				"RecordResponse.event_id",
				"RecordResponse.occurrence_id",
				"RecordResponse.issue_id",
				"RecordResponse.fingerprint",
				"RecordResponse.created",
				"FeedbackRequest.schema_version",
				"FeedbackRequest.issue_id",
				"FeedbackRequest.solution_version_id",
				"FeedbackRequest.outcome",
				"FeedbackResponse.schema_version",
				"FeedbackResponse.outcome_id",
				"FeedbackResponse.recorded",
				"ListRequest.schema_version",
				"ListRequest.kind",
				"ListResponse.schema_version",
				"ListResponse.items",
				"StatsRequest.schema_version",
				"StatsResponse.schema_version",
				"StatsResponse.totals",
				"StatsResponse.totals.events",
				"StatsResponse.totals.issues",
				"StatsResponse.totals.solutions",
				"StatsResponse.totals.outcomes",
				"StatsResponse.retrieval",
				"StatsResponse.retrieval.p50_ms",
				"StatsResponse.retrieval.p95_ms",
				"StatsResponse.tokens",
				"StatsResponse.tokens.estimated_saved",
				"StatsResponse.tokens.measured_saved",
				"StatsResponse.tokens.measured_events",
				"Candidate.issue_id",
				"Candidate.fingerprint",
				"Candidate.title",
				"Candidate.match_reason",
				"Candidate.match_detail",
				"Candidate.approximate",
				"Candidate.confidence",
				"Solution.solution_version_id",
				"Solution.body",
				"Solution.source",
				"Solution.applies_to",
				"Solution.trust",
				"ListItem.id",
				"ListItem.title",
				"ListItem.updated_at",
				"Tool.name",
				"Tool.description",
				"Tool.read_only",
				"Tool.input",
				"RecordSolutionRequest.schema_version",
				"RecordSolutionRequest.issue_id",
				"RecordSolutionRequest.body",
				"RecordSolutionResponse.schema_version",
				"RecordSolutionResponse.solution_id",
				"RecordSolutionResponse.solution_version_id",
				"RecordSolutionResponse.version",
				"RecordSolutionResponse.recorded",
				"SetStatusRequest.schema_version",
				"SetStatusRequest.issue_id",
				"SetStatusRequest.status",
				"SetStatusResponse.schema_version",
				"SetStatusResponse.issue_id",
				"SetStatusResponse.status",
				"Tool.output",
			]
			enums: {
				match_reason: ["issue_id", "error_code", "exact_fingerprint", "deterministic_filter", "trigram_candidate", "jaccard_rerank"]
				outcome: ["success", "failure", "unknown"]
				list_kind: ["issue", "solution"]
				issue_status: ["open", "resolved", "wontfix", "misjudged"]
			}
		}
		prevention: {
			file: "spec/prevention.cue"
			definitions: ["PreventionAddRequest", "PreventionAddResponse", "PreventionVerifyRequest", "PreventionVerifyResponse", "PreventionActivateRequest", "PreventionActivateResponse", "PreventionListRequest", "PreventionListResponse", "PreventionGetRequest", "PreventionGetResponse", "PreventionStatsRequest", "PreventionStatsResponse", "PreventionArtifact", "PreventionVersion", "PreventionVerification", "PreventionArtifactView", "PreventionCount", "PreventionActivationStats"]
			schema_version: "v1"
			semantics: "Local prevention metadata: register immutable repository-relative artifact versions, record explicit CLI verification and activation, and observe real recurrence without storing artifact bodies or repository absolute paths."
			unknown_fields: "reject"
			decoder: {file: "internal/contract/local_api.go", marker: "DisallowUnknownFields"}
			version_anchor: "#PreventionSchemaVersion"
			fields: [
				"PreventionAddRequest.schema_version", "PreventionAddRequest.issue_id", "PreventionAddRequest.kind", "PreventionAddRequest.title", "PreventionAddRequest.artifact_path", "PreventionAddRequest.content_sha256", "PreventionAddRequest.source", "PreventionAddRequest.actor",
				"PreventionAddResponse.schema_version", "PreventionAddResponse.artifact", "PreventionAddResponse.version", "PreventionAddResponse.created",
				"PreventionVerifyRequest.schema_version", "PreventionVerifyRequest.artifact_version_id", "PreventionVerifyRequest.repo_path", "PreventionVerifyRequest.command",
				"PreventionVerifyResponse.schema_version", "PreventionVerifyResponse.verification", "PreventionVerifyResponse.version",
				"PreventionActivateRequest.schema_version", "PreventionActivateRequest.artifact_version_id", "PreventionActivateRequest.repo_path",
				"PreventionActivateResponse.schema_version", "PreventionActivateResponse.version", "PreventionActivateResponse.activated",
				"PreventionListRequest.schema_version", "PreventionListRequest.issue_id", "PreventionListRequest.project_key", "PreventionListRequest.kind", "PreventionListRequest.status", "PreventionListRequest.limit", "PreventionListRequest.cursor",
				"PreventionListResponse.schema_version", "PreventionListResponse.items", "PreventionListResponse.next_cursor",
				"PreventionGetRequest.schema_version", "PreventionGetRequest.artifact_id",
				"PreventionGetResponse.schema_version", "PreventionGetResponse.artifact", "PreventionGetResponse.versions", "PreventionGetResponse.verifications",
				"PreventionStatsRequest.schema_version", "PreventionStatsRequest.project_key", "PreventionStatsRequest.issue_id",
				"PreventionStatsResponse.schema_version", "PreventionStatsResponse.by_status", "PreventionStatsResponse.by_kind", "PreventionStatsResponse.verification_runs", "PreventionStatsResponse.verification_passed", "PreventionStatsResponse.verification_pass_rate", "PreventionStatsResponse.activations", "PreventionStatsResponse.mature_activations", "PreventionStatsResponse.mature_zero_recurrence", "PreventionStatsResponse.mature_protection_rate", "PreventionStatsResponse.recurring_version_ids",
				"PreventionArtifact.artifact_id", "PreventionArtifact.project_key", "PreventionArtifact.kind", "PreventionArtifact.title", "PreventionArtifact.issue_ids", "PreventionArtifact.created_at", "PreventionArtifact.updated_at",
				"PreventionVersion.artifact_version_id", "PreventionVersion.artifact_id", "PreventionVersion.version", "PreventionVersion.artifact_path", "PreventionVersion.content_sha256", "PreventionVersion.status", "PreventionVersion.commit_sha", "PreventionVersion.activated_at", "PreventionVersion.created_at", "PreventionVersion.latest_verification",
				"PreventionVerification.verification_id", "PreventionVerification.artifact_version_id", "PreventionVerification.command", "PreventionVerification.exit_code", "PreventionVerification.passed", "PreventionVerification.duration_ms", "PreventionVerification.evidence", "PreventionVerification.environment", "PreventionVerification.environment.os", "PreventionVerification.environment.shell", "PreventionVerification.environment.go_version", "PreventionVerification.environment.git_version", "PreventionVerification.content_sha256", "PreventionVerification.created_at",
				"PreventionArtifactView.artifact", "PreventionArtifactView.versions",
				"PreventionCount.name", "PreventionCount.count",
				"PreventionActivationStats.issue_id", "PreventionActivationStats.artifact_id", "PreventionActivationStats.artifact_version_id", "PreventionActivationStats.activated_at", "PreventionActivationStats.commit_sha", "PreventionActivationStats.status", "PreventionActivationStats.first_seen_to_activation_ms", "PreventionActivationStats.recurrence_count", "PreventionActivationStats.before_count", "PreventionActivationStats.after_count", "PreventionActivationStats.before_days", "PreventionActivationStats.after_days", "PreventionActivationStats.observation_status",
			]
			required: [
				"PreventionAddRequest.schema_version", "PreventionAddRequest.issue_id", "PreventionAddRequest.kind", "PreventionAddRequest.title", "PreventionAddRequest.artifact_path", "PreventionAddRequest.content_sha256",
				"PreventionAddResponse.schema_version", "PreventionAddResponse.artifact", "PreventionAddResponse.version", "PreventionAddResponse.created",
				"PreventionVerifyRequest.schema_version", "PreventionVerifyRequest.artifact_version_id", "PreventionVerifyRequest.repo_path", "PreventionVerifyRequest.command",
				"PreventionVerifyResponse.schema_version", "PreventionVerifyResponse.verification", "PreventionVerifyResponse.version",
				"PreventionActivateRequest.schema_version", "PreventionActivateRequest.artifact_version_id", "PreventionActivateRequest.repo_path",
				"PreventionActivateResponse.schema_version", "PreventionActivateResponse.version", "PreventionActivateResponse.activated",
				"PreventionListRequest.schema_version", "PreventionListResponse.schema_version", "PreventionListResponse.items",
				"PreventionGetRequest.schema_version", "PreventionGetRequest.artifact_id", "PreventionGetResponse.schema_version", "PreventionGetResponse.artifact", "PreventionGetResponse.versions", "PreventionGetResponse.verifications",
				"PreventionStatsRequest.schema_version", "PreventionStatsResponse.schema_version", "PreventionStatsResponse.by_status", "PreventionStatsResponse.by_kind", "PreventionStatsResponse.verification_runs", "PreventionStatsResponse.verification_passed", "PreventionStatsResponse.verification_pass_rate", "PreventionStatsResponse.activations", "PreventionStatsResponse.mature_activations", "PreventionStatsResponse.mature_zero_recurrence", "PreventionStatsResponse.mature_protection_rate", "PreventionStatsResponse.recurring_version_ids",
				"PreventionArtifact.artifact_id", "PreventionArtifact.project_key", "PreventionArtifact.kind", "PreventionArtifact.title", "PreventionArtifact.issue_ids", "PreventionArtifact.created_at", "PreventionArtifact.updated_at",
				"PreventionVersion.artifact_version_id", "PreventionVersion.artifact_id", "PreventionVersion.version", "PreventionVersion.artifact_path", "PreventionVersion.content_sha256", "PreventionVersion.status", "PreventionVersion.created_at",
				"PreventionVerification.verification_id", "PreventionVerification.artifact_version_id", "PreventionVerification.command", "PreventionVerification.exit_code", "PreventionVerification.passed", "PreventionVerification.duration_ms", "PreventionVerification.evidence", "PreventionVerification.environment", "PreventionVerification.content_sha256", "PreventionVerification.created_at",
				"PreventionArtifactView.artifact", "PreventionArtifactView.versions", "PreventionCount.name", "PreventionCount.count",
				"PreventionActivationStats.issue_id", "PreventionActivationStats.artifact_id", "PreventionActivationStats.artifact_version_id", "PreventionActivationStats.activated_at", "PreventionActivationStats.commit_sha", "PreventionActivationStats.status", "PreventionActivationStats.first_seen_to_activation_ms", "PreventionActivationStats.recurrence_count", "PreventionActivationStats.before_count", "PreventionActivationStats.after_count", "PreventionActivationStats.before_days", "PreventionActivationStats.after_days", "PreventionActivationStats.observation_status",
			]
			enums: {
				prevention_kind: ["regression_test", "deterministic_check", "agent_instruction", "intent"]
				prevention_status: ["draft", "verified", "active", "deprecated"]
				prevention_observation_status: ["observing", "mature"]
			}
		}
		local_api: {
			file: "spec/local-api.cue"
			definitions: ["Request", "Response", "LocalError"]
			schema_version: "v1"
			semantics:      "One newline-delimited JSON envelope on the local named pipe: a versioned envelope with request id, operation and exactly one of result and error; payloads reuse the MCP tool definitions verbatim."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/local_api.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#EnvelopeVersion"
			fields: [
				"Request.schema_version",
				"Request.request_id",
				"Request.operation",
				"Request.payload",
				"Response.schema_version",
				"Response.request_id",
				"Response.operation",
				"Response.result",
				"Response.error",
				"LocalError.code",
				"LocalError.message",
				"LocalError.retryable",
			]
			required: [
				"Request.schema_version",
				"Request.request_id",
				"Request.operation",
				"Request.payload",
				"Response.schema_version",
				"Response.request_id",
				"Response.operation",
				"LocalError.code",
				"LocalError.message",
				"LocalError.retryable",
			]
			enums: {
				operation: ["lookup", "search", "record", "feedback", "list", "stats", "capture", "record_solution", "set_status", "get_issue", "set_solution_status", "delete_solution", "prevention_add", "prevention_verify", "prevention_activate", "prevention_list", "prevention_get", "prevention_stats"]
				error_code: ["invalid_request", "not_found", "unavailable", "conflict", "internal"]
			}
		}
		// The detail view (WF-005): one issue with its occurrence, solution and
		// outcome history plus the two solution writes. The reads and writes reuse
		// the closed state machine of internal/service/edit_api.go; a status change
		// to deprecated or blocked and any deletion still need confirmed=true.
		detail_view: {
			file: "spec/detail-view.cue"
			definitions: ["GetIssueRequest", "GetIssueResponse", "SetSolutionStatusRequest", "SetSolutionStatusResponse", "DeleteSolutionRequest", "DeleteSolutionResponse"]
			schema_version: "v1"
			semantics:      "One issue with its history for the Web toolbox, plus the solution status and deletion writes. The view types are display-only; trust stays computed by errkb_trust() and every write is audited."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/local_api.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#DetailViewVersion"
			fields: [
				"GetIssueRequest.schema_version",
				"GetIssueRequest.issue_id",
				"GetIssueResponse.schema_version",
				"GetIssueResponse.issue",
				"SetSolutionStatusRequest.schema_version",
				"SetSolutionStatusRequest.solution_id",
				"SetSolutionStatusRequest.status",
				"SetSolutionStatusRequest.confirmed",
				"SetSolutionStatusResponse.schema_version",
				"SetSolutionStatusResponse.solution_id",
				"SetSolutionStatusResponse.status",
				"DeleteSolutionRequest.schema_version",
				"DeleteSolutionRequest.solution_id",
				"DeleteSolutionRequest.confirmed",
				"DeleteSolutionResponse.schema_version",
				"DeleteSolutionResponse.solution_id",
				"DeleteSolutionResponse.deleted",
			]
			required: [
				"GetIssueRequest.schema_version",
				"GetIssueRequest.issue_id",
				"GetIssueResponse.schema_version",
				"GetIssueResponse.issue",
				"SetSolutionStatusRequest.schema_version",
				"SetSolutionStatusRequest.solution_id",
				"SetSolutionStatusRequest.status",
				"SetSolutionStatusResponse.schema_version",
				"SetSolutionStatusResponse.solution_id",
				"SetSolutionStatusResponse.status",
				"DeleteSolutionRequest.schema_version",
				"DeleteSolutionRequest.solution_id",
				"DeleteSolutionResponse.schema_version",
				"DeleteSolutionResponse.solution_id",
				"DeleteSolutionResponse.deleted",
			]
		}
		redact: {
			file: "spec/redact.cue"
			definitions: ["Redact"]
			schema_version: "v1"
			semantics:      "The redaction rules document: every credential and personal-data family, its replacement placeholder, the public and private policies, the home-prefix handling and the privacy-guard alternatives. It is a build-time document: the generated Go and TypeScript artifacts are what run, and the conformance corpus proves they agree."
			unknown_fields: "tolerate"
			decoder: {
				file:   "internal/redact/rules_gen.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#RedactVersion"
			fields: [
				"schema_version",
				"placeholders",
				"rules",
				"home_prefix",
				"path_guards",
			]
			required: [
				"schema_version",
				"placeholders",
				"rules",
				"home_prefix",
				"path_guards",
			]
		}
	}
	// Upgrade guarantees (EKB-053).
	//
	// The upgrade tests in internal/database/upgrade_test.go load artifacts that
	// were produced by an earlier release and check that the current build keeps
	// them working:
	//   - a real PostgreSQL 16.15 schema dump at migration 0007 is migrated to
	//     0009 with the data intact;
	//   - a configuration file written before the optional templates block existed
	//     loads unchanged and still validates;
	//   - the TypeScript-era ERRORS.md imports without loss, and a document with
	//     two solution versions keeps its history;
	//   - a capability file from the first connector release loads, and its missing
	//     fields remain an explicit degradation.
	//
	// Every fixture is committed, so the checks are reproducible from the repo and
	// do not depend on a machine created in the past.
	upgrade: {
		schema: {
			ledger:    "schema_migrations"
			from:      "0007"
			to:        "0009"
			mode:      "forward only, one transaction per migration"
			direct:    true
			data:      "existing rows are preserved; a new column is nullable or carries a default"
			downgrade: "unsupported: a database migrated past a release must not be opened by an older binary"
			future:    "a ledger version the binary does not know stops startup with an explicit schema migration error"
		}
		config: {
			format:         "yaml"
			unknown_fields: "reject (yaml KnownFields): an unknown key is a typo or a newer file, never silently ignored"
			old_files:      "a file written before an optional block was added loads unchanged and leaves that block at its defaults"
			new_fields:     "an added field must be optional with a default and must not repurpose an existing key"
		}
		markdown: {
			format:        "ERRORS.md v1 (parser and renderer in internal/database/markdown.go)"
			old_files:     "files written by the TypeScript exporter and by older errkb binaries import without loss"
			multi_version: "the newest body renders inline and older versions follow the history comment, replayed on first import"
			redaction:     "import redacts before every write; stored text never keeps a raw credential"
			idempotent:    "re-importing the same document does not duplicate issues, events or versions"
		}
		connector: {
			protocol:      "v1 (spec/connector.cue #ConnectorSDKVersion)"
			old_artifacts: "capability files from earlier releases load and validate; missing fields stay explicit"
			mismatch:      "a schema_version or channel the core does not know is rejected with an error, never downgraded silently"
			degradation:   "a connector that cannot capture a field declares the fallback channel and the exact field loss"
		}
		matrix: [
			{from: "0001-0006", to: "0009", path: "direct", note: "the migrator applies every missing file in version order"},
			{from: "0007", to: "0009", path: "direct", note: "nullable duration_ms; rows written before the upgrade stay valid and are excluded from the percentile"},
			{from: "config v1 before templates", to: "config v1 with templates", path: "direct", note: "templates is optional, so the old file loads with it unset"},
			{from: "ERRORS.md v1 single version", to: "ERRORS.md v1 multi version", path: "direct", note: "an imported issue gains its versions in the order the document lists them"},
			{from: "connector v1 with a partial capability", to: "connector v1", path: "direct", note: "missing fields stay declared and the fallback carries the loss"},
			{from: "a newer schema or protocol version", to: "this binary", path: "rejected", note: "refuse what is not understood instead of guessing"},
		]
		fixtures: [
			{path: "db/migrations/testdata/upgrade/schema-0007.sql", kind: "schema dump", source: "pg_dump --schema-only of a PostgreSQL 16.15 database migrated to 0007, ledger rows included", commit: "84f8895"},
			{path: "db/migrations/testdata/upgrade/config-v0.yaml", kind: "configuration", source: "yaml.Marshal of internal/contract.Config before the templates block existed", commit: "5ad354d"},
			{path: "db/migrations/testdata/upgrade/ERRORS-v0.md", kind: "markdown", source: "seeds/ERRORS.seed.md written by the TypeScript exporter", commit: "aad0835"},
			{path: "db/migrations/testdata/upgrade/ERRORS-history.md", kind: "markdown", source: "internal/database.ExportMarkdown with two solution versions", commit: "84f8895"},
			{path: "db/migrations/testdata/upgrade/capability-v0.json", kind: "capability", source: "connectors/claude/capability.json from the first connector release", commit: "fcc01eb"},
		]
	}
}
