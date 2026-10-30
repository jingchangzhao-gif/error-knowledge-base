package spec

import "list"

#PreventionSchemaVersion: "v1"
#PreventionKind: "regression_test" | "deterministic_check" | "agent_instruction" | "intent"
#PreventionStatus: "draft" | "verified" | "active" | "deprecated"
#PreventionObservationStatus: "observing" | "mature"
#PreventionTimestamp: string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
#PreventionSHA256: string & =~"^[0-9a-f]{64}$"
#PreventionCommitSHA: string & =~"^[0-9a-f]{40}$"
#PreventionArtifactPath: string & !="" & !~"^/" & !~"^[A-Za-z]:" & !~"\\\\" & !~"//" & !~"(^|/)(\\.|\\.\\.|(?i:\\.git))(/|$)"

#PreventionAddRequest: {
	schema_version: #PreventionSchemaVersion
	issue_id: string & !=""
	kind: #PreventionKind
	title: #NoSensitive & !=""
	artifact_path: #PreventionArtifactPath
	content_sha256: #PreventionSHA256
	source?: #NoSensitive & !=""
	actor?: #NoSensitive & !=""
}

#PreventionVerifyRequest: {
	schema_version: #PreventionSchemaVersion
	artifact_version_id: string & !=""
	repo_path: string & !=""
	command: [...string & !=""] & list.MinItems(1)
}

#PreventionActivateRequest: {
	schema_version: #PreventionSchemaVersion
	artifact_version_id: string & !=""
	repo_path: string & !=""
}

#PreventionListRequest: {
	schema_version: #PreventionSchemaVersion
	issue_id?: string & !=""
	project_key?: string & !=""
	kind?: #PreventionKind
	status?: #PreventionStatus
	limit?: int & >=1 & <=100
	cursor?: string & !=""
}

#PreventionGetRequest: {
	schema_version: #PreventionSchemaVersion
	artifact_id: string & !=""
}

#PreventionStatsRequest: {
	schema_version: #PreventionSchemaVersion
	project_key?: string & !=""
	issue_id?: string & !=""
}

#PreventionArtifact: {
	artifact_id: string & !=""
	project_key: string & !=""
	kind: #PreventionKind
	title: #NoSensitive & !=""
	issue_ids: [...string & !=""]
	created_at: #PreventionTimestamp
	updated_at: #PreventionTimestamp
}

#PreventionVersion: {
	artifact_version_id: string & !=""
	artifact_id: string & !=""
	version: int & >=1
	artifact_path: #PreventionArtifactPath
	content_sha256: #PreventionSHA256
	status: #PreventionStatus
	commit_sha?: #PreventionCommitSHA
	activated_at?: #PreventionTimestamp
	created_at: #PreventionTimestamp
	latest_verification?: #PreventionVerification
	if status == "active" {
		commit_sha: #PreventionCommitSHA
		activated_at: #PreventionTimestamp
	}
	if status == "draft" || status == "verified" {
		commit_sha?: _|_
		activated_at?: _|_
	}
}

#PreventionVerification: {
	verification_id: string & !=""
	artifact_version_id: string & !=""
	command: [...#NoSensitive & !=""] & list.MinItems(1)
	exit_code: int | null
	passed: bool
	duration_ms: int & >=0
	evidence: #NoSensitive
	environment: {
		os?: string
		shell?: string
		go_version?: string
		git_version?: string
	}
	content_sha256: #PreventionSHA256
	created_at: #PreventionTimestamp
}

#PreventionArtifactView: {
	artifact: #PreventionArtifact
	versions: [...#PreventionVersion]
}

#PreventionAddResponse: {
	schema_version: #PreventionSchemaVersion
	artifact: #PreventionArtifact
	version: #PreventionVersion
	created: bool
}

#PreventionVerifyResponse: {
	schema_version: #PreventionSchemaVersion
	verification: #PreventionVerification
	version: #PreventionVersion
}

#PreventionActivateResponse: {
	schema_version: #PreventionSchemaVersion
	version: #PreventionVersion
	activated: bool
}

#PreventionListResponse: {
	schema_version: #PreventionSchemaVersion
	items: [...#PreventionArtifactView]
	next_cursor?: string & !=""
}

#PreventionGetResponse: {
	schema_version: #PreventionSchemaVersion
	artifact: #PreventionArtifact
	versions: [...#PreventionVersion]
	verifications: [...#PreventionVerification]
}

#PreventionCount: {
	name: string & !=""
	count: int & >=0
}

#PreventionActivationStats: {
	issue_id: string & !=""
	artifact_id: string & !=""
	artifact_version_id: string & !=""
	activated_at: #PreventionTimestamp
	commit_sha: #PreventionCommitSHA
	status: #PreventionStatus
	first_seen_to_activation_ms: int & >=0
	recurrence_count: int & >=0
	before_count: int & >=0
	after_count: int & >=0
	before_days: int & >=0
	after_days: int & >=0
	observation_status: #PreventionObservationStatus
}

#PreventionStatsResponse: {
	schema_version: #PreventionSchemaVersion
	by_status: [...#PreventionCount]
	by_kind: [...#PreventionCount]
	verification_runs: int & >=0
	verification_passed: int & >=0
	verification_pass_rate: >=0 & <=1
	activations: [...#PreventionActivationStats]
	mature_activations: int & >=0
	mature_zero_recurrence: int & >=0
	mature_protection_rate: (>=0 & <=1) | null
	recurring_version_ids: [...string & !=""]
}

#PreventionRegisterTool: #Tool & {
	name: "err_prevention_register"
	description: "Register a draft prevention artifact using a caller-computed content hash. Verification and activation remain CLI-only."
	read_only: false
	input: #PreventionAddRequest
	output: #PreventionAddResponse
}
#PreventionListTool: #Tool & {
	name: "err_prevention_list"
	description: "List prevention artifacts and immutable versions without executing commands or changing state."
	read_only: true
	input: #PreventionListRequest
	output: #PreventionListResponse
}
#PreventionGetTool: #Tool & {
	name: "err_prevention_get"
	description: "Read one prevention artifact, its versions, verification evidence and activation metadata."
	read_only: true
	input: #PreventionGetRequest
	output: #PreventionGetResponse
}
