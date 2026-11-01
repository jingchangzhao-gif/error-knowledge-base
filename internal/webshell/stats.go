// The statistics, audit and settings surface of the Web toolbox (EKB-059).
//
// The numbers come from internal/audit and internal/ui unchanged: this file only
// decodes requests, delegates and encodes responses. Settings edits go through
// ui.SettingsPage, which validates the whole configuration and rolls back the
// in-memory state when the save fails.
package webshell

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/audit"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/ui"
)

// StatsFunc collects one snapshot from the database aggregates.
type StatsFunc func(ctx context.Context) (audit.Stats, error)

// AuditFunc reads one page of the audit trail.
type AuditFunc func(ctx context.Context, filter audit.Filter) (audit.Page, error)

// EnableStatistics wires the statistics and audit reads. Nil keeps the related
// routes answering 503 instead of crashing the shell.
func (s *Server) EnableStatistics(stats StatsFunc, trail AuditFunc) {
	s.stats = stats
	s.audit = trail
}

// EnableSettings wires the settings page. The page owns validation; the shell
// only tracks the pending deletion preview between the two requests.
func (s *Server) EnableSettings(page *ui.SettingsPage) {
	s.settings = page
}

type settingsView struct {
	Status     string `json:"status"`
	Capture    captureView
	Connectors []connectorView `json:"connectors"`
	Retention  retentionView
	HTTP       httpView
	Pipe       pipeView
	Deletion   *deletionView `json:"pending_deletion,omitempty"`
}

type captureView struct {
	Enabled        bool     `json:"enabled"`
	PausedProjects []string `json:"paused_projects"`
}

type connectorView struct {
	Name       string    `json:"name"`
	Channel    string    `json:"channel"`
	Version    string    `json:"version"`
	LastActive time.Time `json:"last_active"`
	Enabled    bool      `json:"enabled"`
}

type retentionView struct {
	SampleDays     int `json:"sample_days"`
	NormalizedDays int `json:"normalized_days"`
	AuditDays      int `json:"audit_days"`
}

type httpView struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	TokenSet bool   `json:"token_set"`
}

type pipeView struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
}

type deletionView struct {
	Counts  deleteCountsView `json:"counts"`
	Summary string           `json:"summary"`
}

type deleteCountsView struct {
	Issues                     int64 `json:"issues"`
	Solutions                  int64 `json:"solutions"`
	SolutionVersions           int64 `json:"solution_versions"`
	Outcomes                   int64 `json:"outcomes"`
	Retrievals                 int64 `json:"retrievals"`
	Notices                    int64 `json:"notices"`
	Occurrences                int64 `json:"occurrences"`
	Events                     int64 `json:"events"`
	PreventionArtifacts        int64 `json:"prevention_artifacts"`
	PreventionArtifactVersions int64 `json:"prevention_artifact_versions"`
	IssuePreventions           int64 `json:"issue_preventions"`
	PreventionVerifications    int64 `json:"prevention_verifications"`
}

func countsView(counts database.DeleteCounts) deleteCountsView {
	return deleteCountsView{
		Issues: counts.Issues, Solutions: counts.Solutions,
		SolutionVersions: counts.SolutionVersions, Outcomes: counts.Outcomes,
		Retrievals: counts.Retrievals, Notices: counts.Notices,
		Occurrences: counts.Occurrences, Events: counts.Events,
		PreventionArtifacts:        counts.PreventionArtifacts,
		PreventionArtifactVersions: counts.PreventionArtifactVersions,
		IssuePreventions:           counts.IssuePreventions,
		PreventionVerifications:    counts.PreventionVerifications,
	}
}

func (s *Server) serveStats(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.stats == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "statistics are not wired")
		return
	}
	stats, err := s.stats(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable", "the statistics could not be read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]audit.Stats{"stats": stats})
}

func (s *Server) serveAudit(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.audit == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the audit trail is not wired")
		return
	}
	filter, err := auditFilterFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	page, err := s.audit(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable", "the audit trail could not be read")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func auditFilterFromQuery(r *http.Request) (audit.Filter, error) {
	query := r.URL.Query()
	filter := audit.Filter{
		Action:   strings.TrimSpace(query.Get("action")),
		Entity:   strings.TrimSpace(query.Get("entity")),
		EntityID: strings.TrimSpace(query.Get("entity_id")),
	}
	parse := func(name string) (time.Time, error) {
		raw := strings.TrimSpace(query.Get(name))
		if raw == "" {
			return time.Time{}, nil
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, err
		}
		return parsed, nil
	}
	var err error
	if filter.From, err = parse("from"); err != nil {
		return audit.Filter{}, err
	}
	if filter.To, err = parse("to"); err != nil {
		return audit.Filter{}, err
	}
	parseInt := func(name string, fallback int) (int, error) {
		raw := strings.TrimSpace(query.Get(name))
		if raw == "" {
			return fallback, nil
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, errInvalidNumber(name)
		}
		return value, nil
	}
	if filter.Limit, err = parseInt("limit", 0); err != nil {
		return audit.Filter{}, err
	}
	if filter.Offset, err = parseInt("offset", 0); err != nil {
		return audit.Filter{}, err
	}
	return filter, nil
}

func errInvalidNumber(name string) error {
	return &queryError{message: name + ": want a non-negative integer"}
}

type queryError struct{ message string }

func (e *queryError) Error() string { return e.message }

func (s *Server) serveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	if err := s.settings.Load(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the settings could not be loaded: "+err.Error())
		return
	}
	enabled, host, token := s.settings.HTTPState()
	pipeEnabled, pipeName := s.settings.PipeState()
	view := settingsView{
		Status:  s.settings.Status(),
		Capture: captureView{Enabled: s.settings.CaptureEnabled(), PausedProjects: s.settings.PausedProjects()},
		HTTP:    httpView{Enabled: enabled, Host: host, TokenSet: strings.TrimSpace(token) != ""},
		Pipe:    pipeView{Enabled: pipeEnabled, Name: pipeName},
	}
	samples, normalized, auditDays := s.settings.RetentionDays()
	view.Retention = retentionView{SampleDays: samples, NormalizedDays: normalized, AuditDays: auditDays}
	connectors := s.settings.Connectors()
	view.Connectors = make([]connectorView, 0, len(connectors))
	for _, connector := range connectors {
		view.Connectors = append(view.Connectors, connectorView{
			Name: connector.Name, Channel: connector.Channel, Version: connector.Version,
			LastActive: connector.LastActive, Enabled: connector.Enabled,
		})
	}
	if counts, ok := s.settings.PendingDeletion(); ok {
		view.Deletion = &deletionView{Counts: countsView(counts), Summary: ui.FormatDeleteCounts(counts)}
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) serveConnectorToggle(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.settings.SetConnectorEnabled(r.Context(), body.Name, body.Enabled); err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) serveCaptureToggle(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.settings.SetCaptureEnabled(r.Context(), body.Enabled); err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) serveProjectToggle(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		Project string `json:"project"`
		Enabled bool   `json:"enabled"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.settings.SetProjectEnabled(r.Context(), body.Project, body.Enabled); err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) serveRetention(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		SampleDays     int `json:"sample_days"`
		NormalizedDays int `json:"normalized_days"`
		AuditDays      int `json:"audit_days"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.settings.SetRetention(r.Context(), body.SampleDays, body.NormalizedDays, body.AuditDays); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) serveHTTPToggle(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.settings.SetHTTP(r.Context(), body.Enabled, body.Token); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (s *Server) serveDeletionPreview(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		ProjectKey  string  `json:"project_key"`
		ConnectorID string  `json:"connector_id"`
		Before      *string `json:"before"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request := ui.DeleteRequest{ProjectKey: strings.TrimSpace(body.ProjectKey), ConnectorID: strings.TrimSpace(body.ConnectorID)}
	if body.Before != nil && strings.TrimSpace(*body.Before) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*body.Before))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "before: want an RFC3339 timestamp")
			return
		}
		request.Before = &parsed
	}
	counts, err := s.settings.PreviewDelete(r.Context(), request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deletionView{Counts: countsView(counts), Summary: ui.FormatDeleteCounts(counts)})
}

func (s *Server) serveDeletionConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	counts, err := s.settings.ConfirmDelete(r.Context())
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deletionView{Counts: countsView(counts), Summary: ui.FormatDeleteCounts(counts)})
}

func (s *Server) serveDeletionCancel(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	s.settings.CancelDelete()
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (s *Server) serveRetentionApply(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing or invalid token")
		return
	}
	if s.settings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "settings are not wired")
		return
	}
	var body struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := s.decodeSettingsBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	report, err := s.settings.ApplyRetention(r.Context(), body.Confirmed)
	if err != nil {
		writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"confirmed": body.Confirmed,
		"report": map[string]int64{
			"samples_cleared": report.SamplesCleared,
			"events_removed":  report.EventsRemoved,
			"audit_rows":      report.AuditRowsRemoved,
		},
	})
}

func (s *Server) decodeSettingsBody(w http.ResponseWriter, r *http.Request, target any) error {
	// Every settings POST behaves like the page: refresh first, because the edit
	// functions validate against the current configuration and connector list.
	if s.settings != nil {
		if err := s.settings.Load(r.Context()); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return &queryError{message: "only one JSON document is allowed"}
	}
	return nil
}
