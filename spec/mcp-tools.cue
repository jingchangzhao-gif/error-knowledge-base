// The six MCP tool contracts (EKB-008). One semantics serves both MCP stdio
// and the local Named Pipe API: spec/local-api.cue reuses these definitions
// verbatim inside its envelope, so the two channels cannot drift. Each
// operation carries its own schema_version so the six can move independently
// (EKB-011 keys its compatibility rules on that field).
package spec

import "list"

// #MaxCandidates is the plan section 5 ceiling. It is a contract rule, not an
// implementation limit: a response with four candidates is invalid.
#MaxCandidates: 3

// #Tool is the descriptor shape the MCP server publishes. EKB-037 fills in the
// server; this file fixes the shape it must publish.
#Tool: {
	name:        string
	description: string
	read_only:   bool
	input: {...}
	output: {...}
}

// #MatchReason names the step of the section 5 order that produced the hit.
#MatchReason:
	"issue_id" |
	"error_code" |
	"exact_fingerprint" |
	"deterministic_filter" |
	"trigram_candidate" |
	"jaccard_rerank"

// #Solution is the verified fix a candidate carries (EKB-035). It is optional:
// a brand-new issue has none, and the caller must see that absence instead of
// an empty string. Only verified versions are attached - an unverified
// candidate is not advice. trust is errkb_trust() for the queried environment,
// not a value the caller may set.
#Solution: {
	solution_version_id: string & !=""
	body:                #NoSensitive & !=""
	source:              string & !=""
	applies_to:          string
	trust:               >=0 & <=1
}

// #Candidate is one reason-carrying hit. approximate separates "this is the
// same error" from "this might be": only the first three reasons are exact,
// and an approximate candidate may never auto-apply a fix.
#Candidate: {
	issue_id:     string & !=""
	fingerprint:  #Fingerprint
	title:        #NoSensitive & !=""
	match_reason: #MatchReason
	match_detail: string & !=""
	approximate:  bool
	confidence:   >=0 & <=1
	hits?:        int & >=0
	solution?:    #Solution

	if match_reason == "issue_id" || match_reason == "error_code" || match_reason == "exact_fingerprint" {
		approximate: false
	}
	if match_reason == "deterministic_filter" || match_reason == "trigram_candidate" || match_reason == "jaccard_rerank" {
		approximate: true
	}
}

#CandidateList: [...#Candidate] & list.MaxItems(#MaxCandidates)

// #LookupRequest asks the question "I know what this error is".
#LookupRequest: {
	schema_version: "v1"
	issue?:         string & !=""
	error_code?:    string & !=""
	fingerprint?:   #Fingerprint
	// Raw host text is accepted: a lookup is a query, and the daemon
	// normalizes it before matching. Only stored text carries #NoSensitive.
	normalized_message?: string & !=""
	project_key?:        string & !=""
}

#LookupResponse: {
	schema_version: "v1"
	candidates:     #CandidateList
}

#LookupTool: #Tool & {
	name:        "err_lookup"
	description: "Find known issues by issue id, error code or exact fingerprint. Returns at most three candidates with the match reason."
	read_only:   true
	input:       #LookupRequest
	output:      #LookupResponse
}

// #SearchRequest asks the free-text question. It is not the same operation as
// lookup: the plan forbids implementing both with one fuzzy text query.
#SearchRequest: {
	schema_version: "v1"
	// Raw host text is accepted: a search is a query, and the daemon
	// normalizes it before matching.
	query:        string & !=""
	project_key?: string & !=""
	limit?:       int & >=1 & <=#MaxCandidates
}

#SearchResponse: {
	schema_version: "v1"
	candidates:     #CandidateList
}

#SearchTool: #Tool & {
	name:        "err_search"
	description: "Search known issues by free text. Returns at most three candidates with the match reason and whether each is approximate."
	read_only:   true
	input:       #SearchRequest
	output:      #SearchResponse
}

// #RecordRequest submits one redacted observation. The event contract is
// reused from spec/error-event.cue, so both channels accept the same fields.
#RecordRequest: {
	schema_version: "v1"
	event:          #ErrorEvent
}

#RecordResponse: {
	schema_version: "v1"
	event_id:       string & !=""
	occurrence_id:  string
	issue_id:       string
	fingerprint:    #Fingerprint
	created:        bool
	queued?:        bool
	// A queued write (database unreachable) leaves the occurrence and issue
	// empty until the replay runs; every other response must carry them.
	if !queued {
		occurrence_id: string & !=""
		issue_id:      string & !=""
	}
}

#RecordTool: #Tool & {
	name:        "err_record"
	description: "Record one redacted ErrorEvent v1 and attach it to an issue."
	read_only:   false
	input:       #RecordRequest
	output:      #RecordResponse
}

// #Outcome is the only thing feedback may submit. Trust is computed by
// errkb_trust() from outcomes (plan section 8.2); no tool may set it.
#Outcome: "success" | "failure" | "unknown"

#FeedbackRequest: {
	schema_version:      "v1"
	issue_id:            string & !=""
	solution_version_id: string & !=""
	outcome:             #Outcome
	note?:               #NoSensitive
}

#FeedbackResponse: {
	schema_version: "v1"
	outcome_id:     string & !=""
	recorded:       bool
}

#FeedbackTool: #Tool & {
	name:        "err_feedback"
	description: "Report whether a solution worked in this environment. It records an outcome; it does not set trust."
	read_only:   false
	input:       #FeedbackRequest
	output:      #FeedbackResponse
}

#ListKind: "issue" | "solution"

#ListRequest: {
	schema_version: "v1"
	kind:           #ListKind
	project_key?:   string & !=""
	limit?:         int & >=1 & <=100
	cursor?:        string & !=""
	environment?:   #SolutionEnvironment
}

#ListItem: {
	id:         string & !=""
	title:      #NoSensitive & !=""
	updated_at: string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
	hits?:      int & >=0
	status?:    #IssueStatus
	fix?:       #NoSensitive
}

#ListResponse: {
	schema_version: "v1"
	items: [...#ListItem]
	next_cursor?: string & !=""
}

#ListTool: #Tool & {
	name:        "err_list"
	description: "List known issues or solutions with pagination."
	read_only:   true
	input:       #ListRequest
	output:      #ListResponse
}

#StatsRequest: {
	schema_version: "v1"
	project_key?:   string & !=""
}

// #StatsResponse keeps the two token numbers apart on purpose: estimated
// savings are a heuristic, measured savings only exist where the host reports
// them. Merging the two would overstate the product.
#StatsResponse: {
	schema_version: "v1"
	totals: {
		events:    int & >=0
		issues:    int & >=0
		solutions: int & >=0
		outcomes:  int & >=0
	}
	retrieval: {
		p50_ms: int & >=0
		p95_ms: int & >=0
	}
	tokens: {
		estimated_saved: int & >=0
		measured_saved:  int & >=0
		measured_events: int & >=0
	}
}

#StatsTool: #Tool & {
	name:        "err_stats"
	description: "Report knowledge-base totals, retrieval latency and token savings, keeping estimated and measured savings separate."
	read_only:   true
	input:       #StatsRequest
	output:      #StatsResponse
}

// #IssueStatus is the closed issue-status set (db/migrations/0001_init.sql).
#IssueStatus: "open" | "resolved" | "wontfix" | "misjudged"

// #SolutionEnvironment narrows the hosts a solution applies to. An empty
// object means "no restriction", never "unknown".
#SolutionEnvironment: {
	os?:        string & !=""
	shell?:     string & !=""
	connector?: string & !=""
	runtime_versions?: [string]: string & !=""
}

// #RecordSolutionRequest records a fix against an issue. The core opens (or
// reuses) the solution, appends a new verified version, and marks the issue
// resolved. Trust stays computed by errkb_trust() from outcomes: nothing in
// this request can set it.
#RecordSolutionRequest: {
	schema_version: "v1"
	issue_id:       string & !=""
	body:           #NoSensitive & !=""
	preconditions?: #NoSensitive
	environment?:   #SolutionEnvironment
	source?:        string & !=""
	actor?:         string & !=""
}

#RecordSolutionResponse: {
	schema_version:      "v1"
	solution_id:         string & !=""
	solution_version_id: string & !=""
	version:             int & >=1
	recorded:            bool
}

#RecordSolutionTool: #Tool & {
	name:        "err_record_solution"
	description: "Record a fix for an issue: appends a solution version, verifies it and marks the issue resolved."
	read_only:   false
	input:       #RecordSolutionRequest
	output:      #RecordSolutionResponse
}

// #SetStatusRequest changes an issue's status. wontfix keeps an issue out of
// injection; misjudged marks a recorded failure that was not a real one.
#SetStatusRequest: {
	schema_version: "v1"
	issue_id:       string & !=""
	status:         #IssueStatus
}

#SetStatusResponse: {
	schema_version: "v1"
	issue_id:       string & !=""
	status:         #IssueStatus
}

#SetStatusTool: #Tool & {
	name:        "err_set_status"
	description: "Set an issue status: open, resolved, wontfix or misjudged."
	read_only:   false
	input:       #SetStatusRequest
	output:      #SetStatusResponse
}

// #Tools lists every descriptor. Prevention verification and activation are
// deliberately absent: only the local CLI may execute commands or activate.
#Tools: [#LookupTool, #SearchTool, #RecordTool, #FeedbackTool, #ListTool, #StatsTool, #RecordSolutionTool, #SetStatusTool, #PreventionRegisterTool, #PreventionListTool, #PreventionGetTool]
