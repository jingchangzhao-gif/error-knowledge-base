// SQLBackend is the PostgreSQL-backed implementation of the local API's
// Backend interface (EKB-035). It is the only place that turns a validated
// contract payload into a database call and back; the pipe layer keeps no
// business logic.
//
// The file is portable (no build tag): the daemon serves it on Windows, and
// tests drive it directly.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// DefaultListLimit is the page size errkb_list uses when the caller omits
// limit. The contract still caps it at 100.
const DefaultListLimit = 20

// SQLBackend answers the local API operations from PostgreSQL. A nil or closed
// pool fails every call with CodeUnavailable instead of panicking.
//
// gate (optional) enforces the capture settings before anything is written, and
// queue (optional) buffers already-redacted events while the database is
// unreachable. Both are wired by the daemon from its configuration.
type SQLBackend struct {
	pool  *pgxpool.Pool
	gate  func(ctx context.Context, connectorID, projectKey string) error
	queue *Queue
}

// NewSQLBackend wires a pool. The pool is not owned: the daemon closes it.
func NewSQLBackend(pool *pgxpool.Pool) *SQLBackend {
	return &SQLBackend{pool: pool}
}

// WithCaptureGate rejects writes the configuration paused, before the database
// call. It returns the backend so the daemon can chain the wiring.
func (b *SQLBackend) WithCaptureGate(gate func(ctx context.Context, connectorID, projectKey string) error) *SQLBackend {
	b.gate = gate
	return b
}

// WithQueue buffers redacted events when the database is unreachable.
func (b *SQLBackend) WithQueue(queue *Queue) *SQLBackend {
	b.queue = queue
	return b
}

// checkCapture returns a conflict error when the settings paused this capture.
func (b *SQLBackend) checkCapture(ctx context.Context, connectorID, projectKey string) error {
	if b == nil || b.gate == nil {
		return nil
	}
	if err := b.gate(ctx, connectorID, projectKey); err != nil {
		return APIErrorf(CodeConflict, "%v", err)
	}
	return nil
}

// databaseUnavailableError keeps the underlying connection error reachable so
// the offline path can tell an outage from a data problem after translation.
type databaseUnavailableError struct{ err error }

func (e *databaseUnavailableError) Error() string {
	return "the database is not available: " + e.err.Error()
}

func (e *databaseUnavailableError) Unwrap() error { return e.err }

func (b *SQLBackend) withConn(ctx context.Context, call func(context.Context, *pgx.Conn) error) error {
	if b == nil || b.pool == nil {
		return APIErrorf(CodeUnavailable, "the database is not available")
	}
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return &databaseUnavailableError{err: err}
	}
	defer conn.Release()
	return call(ctx, conn.Conn())
}

// Lookup answers "I know what this error is". A fingerprint-only lookup runs
// the same five-step retrieval and therefore still records a retrieval row.
func (b *SQLBackend) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.LookupResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var candidates []contract.Candidate
	var degraded bool
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Retrieve(ctx, conn, database.RetrievalRequest{
			IssueID:     request.Issue,
			Fingerprint: request.Fingerprint,
			Message:     normalizeQuery(request.NormalizedMessage),
			ErrorCode:   request.ErrorCode,
			ProjectKey:  request.ProjectKey,
		})
		if err != nil {
			return err
		}
		candidates = mapCandidates(result.Candidates)
		degraded = result.Degraded
		return nil
	})
	if err != nil {
		return contract.LookupResponse{}, translateBackendError("lookup", err)
	}
	_ = degraded // degraded is surfaced by the CLI, not by the contract.
	return contract.LookupResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: candidates}, nil
}

// Search is the free-text lookup. It is a separate operation on purpose.
func (b *SQLBackend) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.SearchResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var candidates []contract.Candidate
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Retrieve(ctx, conn, database.RetrievalRequest{
			Message:    normalizeQuery(request.Query),
			ProjectKey: request.ProjectKey,
		})
		if err != nil {
			return err
		}
		candidates = mapCandidates(result.Candidates)
		return nil
	})
	if err != nil {
		return contract.SearchResponse{}, translateBackendError("search", err)
	}
	return contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: candidates}, nil
}

// Record stores one already redacted failure and answers the issue identity.
// When the capture settings paused this connector or project the write is
// refused; when the database is unreachable the event goes to the offline
// queue and the response says so (queued) instead of pretending it landed.
func (b *SQLBackend) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.RecordResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	if err := b.checkCapture(ctx, request.Event.ConnectorID, request.Event.ProjectKey); err != nil {
		return contract.RecordResponse{}, err
	}
	var stored database.RecordResult
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.RecordEvent(ctx, conn, request.Event)
		if err != nil {
			return err
		}
		stored = result
		return nil
	})
	if err != nil {
		if b.queue != nil && isDatabaseUnreachable(err) {
			if queued, queueErr := b.queue.Enqueue(request.Event); queueErr != nil || !queued {
				return contract.RecordResponse{}, translateBackendError("record", err)
			}
			return contract.RecordResponse{
				SchemaVersion: contract.ToolSchemaVersion,
				EventID:       request.Event.EventID,
				Fingerprint:   request.Event.Fingerprint,
				Created:       false,
				Queued:        true,
			}, nil
		}
		return contract.RecordResponse{}, translateBackendError("record", err)
	}
	return contract.RecordResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		EventID:       stored.EventID,
		OccurrenceID:  stored.OccurrenceID,
		IssueID:       stored.IssueID,
		Fingerprint:   stored.Fingerprint,
		Created:       stored.Created,
	}, nil
}

// Feedback records an outcome. It never sets trust: errkb_trust() derives it.
func (b *SQLBackend) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.FeedbackResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var outcomeID string
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		id, err := database.RecordOutcome(ctx, conn, request.IssueID, request.SolutionVersionID,
			database.OutcomeKind(request.Outcome), request.Note, database.Environment{}, "", "cli", "cli")
		if err != nil {
			return err
		}
		outcomeID = id
		return nil
	})
	if err != nil {
		return contract.FeedbackResponse{}, translateBackendError("feedback", err)
	}
	return contract.FeedbackResponse{SchemaVersion: contract.ToolSchemaVersion, OutcomeID: outcomeID, Recorded: true}, nil
}

// List pages issues or solutions. An empty page is not an error.
func (b *SQLBackend) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.ListResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	limit := DefaultListLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	environment := database.Environment{}
	if request.Environment != nil {
		environment = environmentFromContract(request.Environment)
	}
	var items []database.ListSummary
	var next string
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		var err error
		if request.Kind == "solution" {
			items, next, err = database.ListSolutions(ctx, conn, request.ProjectKey, request.Cursor, limit, environment)
		} else {
			items, next, err = database.ListIssues(ctx, conn, request.ProjectKey, request.Cursor, limit, environment)
		}
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "malformed cursor") {
			return contract.ListResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
		}
		return contract.ListResponse{}, translateBackendError("list", err)
	}
	mapped := make([]contract.ListItem, 0, len(items))
	for _, item := range items {
		hits := item.Hits
		mapped = append(mapped, contract.ListItem{
			ID:        item.ID,
			Title:     item.Title,
			UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
			Hits:      &hits,
			Status:    item.Status,
			Fix:       item.Fix,
		})
	}
	return contract.ListResponse{SchemaVersion: contract.ToolSchemaVersion, Items: mapped, NextCursor: next}, nil
}

// Stats reports totals. Latency and token savings are not measured by the
// store yet, so they stay zero instead of being invented.
func (b *SQLBackend) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.StatsResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var summary database.StatsSummary
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Stats(ctx, conn, request.ProjectKey)
		if err != nil {
			return err
		}
		summary = result
		return nil
	})
	if err != nil {
		return contract.StatsResponse{}, translateBackendError("stats", err)
	}
	return contract.StatsResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Totals: contract.StatsTotals{
			Events:    int(summary.Events),
			Issues:    int(summary.Issues),
			Solutions: int(summary.Solutions),
			Outcomes:  int(summary.Outcomes),
		},
	}, nil
}

// mapCandidates converts retrieval rows into the reason-carrying contract
// shape. The mapping is intentionally mechanical: the reason names the step
// that produced the hit, and approximate follows the reranker's verdict.
func mapCandidates(candidates []database.RetrievalCandidate) []contract.Candidate {
	mapped := make([]contract.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		score := candidate.Score
		hits := candidate.Hits
		reason := matchReason(candidate)
		detail := matchDetail(candidate)
		entry := contract.Candidate{
			IssueID:     candidate.IssueID,
			Fingerprint: candidate.Fingerprint,
			Title:       candidate.Title,
			MatchReason: reason,
			MatchDetail: detail,
			Approximate: candidate.Approximate,
			Confidence:  &score,
			Hits:        &hits,
		}
		if candidate.Solution != nil {
			entry.Solution = &contract.CandidateSolution{
				SolutionVersionID: candidate.Solution.SolutionVersionID,
				Body:              candidate.Solution.Body,
				Source:            candidate.Solution.Source,
				AppliesTo:         candidate.Solution.AppliesTo,
				Trust:             candidate.Solution.Trust,
			}
		}
		mapped = append(mapped, entry)
	}
	return mapped
}

// Capture accepts a connector observation and hands it to the capture pipeline
// (EKB-038 rollback): the core classifies, redacts, fingerprints and stores, so
// the adapter stays capability-free.
func (b *SQLBackend) Capture(ctx context.Context, request contract.CaptureRequest) (contract.CaptureResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.CaptureResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	if err := b.checkCapture(ctx, request.ConnectorID, request.Observation.ProjectKey); err != nil {
		return contract.CaptureResponse{}, err
	}
	var response contract.CaptureResponse
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		stored, err := capture.CaptureObservation(ctx, conn, request)
		if err != nil {
			return err
		}
		response = stored
		return nil
	})
	if err != nil {
		// A raw observation cannot be queued: the event needs the connector and
		// session row ids that only the database can mint, so the caller sees
		// unavailable instead of a queued success it cannot replay.
		return contract.CaptureResponse{}, translateBackendError("capture", err)
	}
	return response, nil
}

// RecordSolution appends a verified solution version and resolves the issue.
// It is the write-back the CLI/MCP/DSH hosts use to close the loop.
func (b *SQLBackend) RecordSolution(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.RecordSolutionResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	source := request.Source
	if source == "" {
		source = "cli"
	}
	actor := request.Actor
	if actor == "" {
		actor = "cli"
	}
	var stored database.SolutionRecord
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.RecordSolution(ctx, conn, request.IssueID, request.Body,
			request.Preconditions, environmentFromContract(request.Environment), source, actor)
		if err != nil {
			return err
		}
		stored = result
		return nil
	})
	if err != nil {
		return contract.RecordSolutionResponse{}, translateBackendError("record_solution", err)
	}
	return contract.RecordSolutionResponse{
		SchemaVersion:     contract.ToolSchemaVersion,
		SolutionID:        stored.SolutionID,
		SolutionVersionID: stored.SolutionVersionID,
		Version:           stored.Version,
		Recorded:          true,
	}, nil
}

// SetStatus corrects an issue status.
func (b *SQLBackend) SetStatus(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.SetStatusResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		return database.SetIssueStatus(ctx, conn, request.IssueID, request.Status)
	})
	if err != nil {
		return contract.SetStatusResponse{}, translateBackendError("set_status", err)
	}
	return contract.SetStatusResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		IssueID:       request.IssueID,
		Status:        request.Status,
	}, nil
}

// environmentFromContract maps the wire environment onto the storage shape.
func environmentFromContract(environment *contract.SolutionEnvironment) database.Environment {
	if environment == nil {
		return database.Environment{}
	}
	return database.Environment{
		OS:              environment.OS,
		Shell:           environment.Shell,
		Connector:       environment.Connector,
		RuntimeVersions: environment.RuntimeVersions,
	}
}

func matchReason(candidate database.RetrievalCandidate) string {
	for _, field := range candidate.MatchedFields {
		switch field {
		case core.MatchFieldFingerprint:
			return "exact_fingerprint"
		case core.MatchFieldErrorCode:
			return "error_code"
		}
	}
	if candidate.Approximate {
		return "jaccard_rerank"
	}
	return "deterministic_filter"
}

func matchDetail(candidate database.RetrievalCandidate) string {
	fields := make([]string, 0, len(candidate.MatchedFields))
	for _, field := range candidate.MatchedFields {
		fields = append(fields, string(field))
	}
	detail := fmt.Sprintf("score %.2f", candidate.Score)
	if len(fields) > 0 {
		detail += "; matched: " + strings.Join(fields, ", ")
	}
	if len(candidate.EnvironmentDiff) > 0 {
		differences := make([]string, 0, len(candidate.EnvironmentDiff))
		for _, difference := range candidate.EnvironmentDiff {
			differences = append(differences, difference.Field)
		}
		detail += "; environment differs: " + strings.Join(differences, ", ")
	}
	return detail
}

// translateBackendError maps storage failures onto the closed wire codes. A
// backend that returns anything else becomes CodeInternal in encodeResult.
func translateBackendError(operation string, err error) error {
	var unavailable *databaseUnavailableError
	if errors.As(err, &unavailable) {
		return APIErrorf(CodeUnavailable, "%s: %v", operation, unavailable.err)
	}
	var apiError *APIError
	if errors.As(err, &apiError) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return APIErrorf(CodeNotFound, "%s: %v", operation, err)
	}
	if errors.Is(err, database.ErrPreventionInvalid) {
		return APIErrorf(CodeInvalidRequest, "%s: %v", operation, err)
	}
	if errors.Is(err, database.ErrPreventionConflict) {
		return APIErrorf(CodeConflict, "%s: %v", operation, err)
	}
	if isDatabaseUnreachable(err) {
		return APIErrorf(CodeUnavailable, "%s: %v", operation, err)
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return APIErrorf(CodeConflict, "%s: %s", operation, pgError.Message)
		case "23503", "23514", "22023", "22P02":
			return APIErrorf(CodeInvalidRequest, "%s: %s", operation, pgError.Message)
		}
	}
	return APIErrorf(CodeInternal, "%s: %v", operation, err)
}

// isDatabaseUnreachable reports whether the write failed because the database
// could not be reached, as opposed to a data or constraint problem. Only this
// class may go to the offline queue: a queued event replays later and must not
// hide a validation error behind an outage message.
func isDatabaseUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var connectError *pgconn.ConnectError
	if errors.As(err, &connectError) {
		return true
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "08000", "08001", "08003", "08006", "08007", "08008",
			"57P01", "57P02", "57P03":
			return true
		}
	}
	var netError net.Error
	return errors.As(err, &netError)
}

// normalizeQuery turns raw host text into the matching form. Queries are not
// stored; the stored side is normalized, so the same pipeline applies before
// comparing.
func normalizeQuery(text string) string {
	if text == "" {
		return ""
	}
	return core.Normalize(text)
}

// GetIssue answers the Web detail page. All reads and the trust computation
// come from DetailService, so the Web layer never re-implements business rules.
func (b *SQLBackend) GetIssue(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.GetIssueResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	if b.pool == nil {
		return contract.GetIssueResponse{}, APIErrorf(CodeUnavailable, "no database pool is configured")
	}
	detail, err := NewDetailService(b.pool).Load(ctx, request.IssueID)
	if err != nil {
		if errors.Is(err, ErrDetailNotFound) {
			return contract.GetIssueResponse{}, APIErrorf(CodeNotFound, "issue %s not found", request.IssueID)
		}
		return contract.GetIssueResponse{}, translateBackendError("get_issue", err)
	}
	return contract.GetIssueResponse{SchemaVersion: contract.SchemaVersion, Issue: mapIssueDetail(detail)}, nil
}

func mapIssueDetail(detail IssueDetail) contract.IssueDetailV {
	out := contract.IssueDetailV{
		ID: detail.Issue.ID, Fingerprint: detail.Issue.Fingerprint, Title: detail.Issue.Title,
		Category: detail.Issue.Category, Status: detail.Issue.Status, ProjectKey: detail.Issue.ProjectKey,
		Trigger: detail.Issue.Trigger, Hits: detail.Issue.Hits,
		FirstSeen: detail.Issue.FirstSeen, LastSeen: detail.Issue.LastSeen,
	}
	for _, occurrence := range detail.Occurrences {
		out.Occurrences = append(out.Occurrences, contract.OccurrenceV{
			ID: occurrence.ID, ObservedAt: occurrence.OccurredAt,
			Environment: detailEnvironmentMap(occurrence.OS, occurrence.Shell, occurrence.Connector, occurrence.RuntimeVersions),
			Headline:    occurrence.Headline,
		})
	}
	for index, solution := range detail.Solutions {
		if index == 0 {
			out.Trust = contract.TrustView{
				Score:     float64(solution.Trust.Score),
				Samples:   int64(solution.Trust.Samples),
				Successes: int64(solution.Trust.Successes),
				Failures:  int64(solution.Trust.Failures),
				Unknowns:  int64(solution.Trust.Unknowns),
			}
		}
		view := contract.SolutionV{ID: solution.ID, Status: solution.Status}
		for _, version := range solution.Versions {
			view.Versions = append(view.Versions, contract.VersionV{
				ID: version.ID, Version: version.Version, Body: version.Body,
				Source: version.Source, AppliesTo: detailEnvironmentText(version.AppliesTo),
				Risks: version.Risks, Preconditions: version.Preconditions, EffectiveAt: version.EffectiveAt,
			})
		}
		out.Solutions = append(out.Solutions, view)
		for _, outcome := range solution.Outcomes {
			out.Outcomes = append(out.Outcomes, contract.OutcomeV{
				ID: outcome.ID, Outcome: outcome.Kind, Note: outcome.Note, ObservedAt: outcome.ObservedAt,
			})
		}
	}
	return out
}

func detailEnvironmentMap(os, shell, connector string, runtimeVersions map[string]string) map[string]string {
	environment := map[string]string{}
	if os != "" {
		environment["os"] = os
	}
	if shell != "" {
		environment["shell"] = shell
	}
	if connector != "" {
		environment["connector"] = connector
	}
	for key, value := range runtimeVersions {
		environment["runtime."+key] = value
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}

func detailEnvironmentText(environment database.Environment) string {
	parts := make([]string, 0, 5)
	for _, pair := range [][2]string{{"os", environment.OS}, {"shell", environment.Shell}, {"connector", environment.Connector}} {
		if pair[1] != "" {
			parts = append(parts, pair[0]+"="+pair[1])
		}
	}
	keys := make([]string, 0, len(environment.RuntimeVersions))
	for key := range environment.RuntimeVersions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, "runtime."+key+"="+environment.RuntimeVersions[key])
	}
	return strings.Join(parts, ", ")
}
