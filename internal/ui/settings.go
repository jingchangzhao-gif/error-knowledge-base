// Package ui contains the testable page models behind the errkb tray, the
// omnibox and the detail view. This file is the settings page: connector
// enablement, capture scope, the three retention periods, scoped deletion with
// a preview, and the network toggles. It deliberately holds no second config
// model — every read and write goes through contract.Config from
// internal/contract/config.go (EKB-007), and every save is re-validated by that
// model before it can reach disk.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// SettingsActor is written into the audit rows of deletions started from the
// settings page, so the audit trail can tell UI deletions from scripted ones.
const SettingsActor = "settings"

// SettingsStore is the only way the page touches the configuration file. Both
// methods go through contract.Config.
type SettingsStore interface {
	Load() (contract.Config, error)
	Save(contract.Config) error
}

// FileSettingsStore keeps the configuration in one YAML file. Save validates
// the marshalled document with contract.LoadConfig before writing it — an
// invalid configuration can never replace a valid one, and the file on disk is
// exactly what the rest of errkb reads.
type FileSettingsStore struct {
	Path string
}

// Load reads and validates the configuration file.
func (s FileSettingsStore) Load() (contract.Config, error) {
	if s.Path == "" {
		return contract.Config{}, errors.New("settings: a configuration path is required")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return contract.Config{}, fmt.Errorf("settings: read %s: %w", s.Path, err)
	}
	config, err := contract.LoadConfig(data)
	if err != nil {
		return contract.Config{}, fmt.Errorf("settings: %w", err)
	}
	return config, nil
}

// Save validates the document through contract.LoadConfig and then replaces
// the file atomically (temporary file in the same directory, then rename).
func (s FileSettingsStore) Save(config contract.Config) error {
	if s.Path == "" {
		return errors.New("settings: a configuration path is required")
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("settings: encode the configuration: %w", err)
	}
	if _, err := contract.LoadConfig(data); err != nil {
		return fmt.Errorf("settings: the change would make the configuration invalid: %w", err)
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("settings: create %s: %w", dir, err)
	}
	temporary := s.Path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		return fmt.Errorf("settings: write %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, s.Path); err != nil {
		return fmt.Errorf("settings: replace %s: %w", s.Path, err)
	}
	return nil
}

// ConnectorSettings is one connector as the settings page shows it: the channel
// level comes from the EKB-043 compatibility matrix and is stored on the
// connector row, the last-active time comes from the newest event.
type ConnectorSettings struct {
	ID         string
	Name       string
	Channel    string
	Version    string
	LastActive time.Time
	Enabled    bool
}

// DeleteRequest selects what a scoped deletion removes. At least one field is
// required; Before is exclusive on the observation time.
type DeleteRequest struct {
	ProjectKey  string
	ConnectorID string
	Before      *time.Time
}

// SettingsDataSource is the database side of the page: the connector list, the
// deletion preview/executor from EKB-026, and the retention pass.
type SettingsDataSource interface {
	Connectors(ctx context.Context) ([]ConnectorSettings, error)
	PreviewDelete(ctx context.Context, scope database.DeleteScope) (database.DeleteCounts, error)
	DeleteScoped(ctx context.Context, scope database.DeleteScope) (database.DeleteCounts, error)
	ApplyRetention(ctx context.Context, policy database.RetentionPolicy, now time.Time) (database.RetentionReport, error)
}

// PostgresSettings is the real data source. It delegates deletion and retention
// to EKB-026's database functions so the preview, the audit row and the SQL
// stay in one place.
type PostgresSettings struct {
	Pool *pgxpool.Pool
}

func (s *PostgresSettings) withConn(ctx context.Context, use func(context.Context, *pgx.Conn) error) error {
	if s == nil || s.Pool == nil {
		return errors.New("settings: the database is not available")
	}
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("settings: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()
	return use(ctx, conn.Conn())
}

// Connectors lists every connector with its channel, version and last activity.
func (s *PostgresSettings) Connectors(ctx context.Context) ([]ConnectorSettings, error) {
	connectors := make([]ConnectorSettings, 0, 8)
	err := s.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		rows, err := conn.Query(ctx, `
			select c.id::text, c.name, c.channel, c.version,
			       coalesce(max(e.observed_at), c.created_at)
			from connectors c
			left join events e on e.connector_id = c.id
			group by c.id, c.name, c.channel, c.version, c.created_at
			order by c.name
		`)
		if err != nil {
			return fmt.Errorf("settings: list connectors: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var connector ConnectorSettings
			if err := rows.Scan(&connector.ID, &connector.Name, &connector.Channel,
				&connector.Version, &connector.LastActive); err != nil {
				return fmt.Errorf("settings: scan connector: %w", err)
			}
			connectors = append(connectors, connector)
		}
		return rows.Err()
	})
	return connectors, err
}

// PreviewDelete counts what a scoped deletion would remove. Nothing is deleted.
func (s *PostgresSettings) PreviewDelete(ctx context.Context, scope database.DeleteScope) (database.DeleteCounts, error) {
	var counts database.DeleteCounts
	err := s.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		previewed, err := database.PreviewDelete(ctx, conn, scope)
		if err != nil {
			return err
		}
		counts = previewed
		return nil
	})
	return counts, err
}

// DeleteScoped removes the selected data and the audit row for the operation.
func (s *PostgresSettings) DeleteScoped(ctx context.Context, scope database.DeleteScope) (database.DeleteCounts, error) {
	var counts database.DeleteCounts
	err := s.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		deleted, err := database.DeleteScoped(ctx, conn, scope)
		if err != nil {
			return err
		}
		counts = deleted
		return nil
	})
	return counts, err
}

// ApplyRetention runs the three retention classes once with the given policy.
func (s *PostgresSettings) ApplyRetention(ctx context.Context, policy database.RetentionPolicy, now time.Time) (database.RetentionReport, error) {
	var report database.RetentionReport
	err := s.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		applied, err := database.ApplyRetention(ctx, conn, policy, now)
		if err != nil {
			return err
		}
		report = applied
		return nil
	})
	return report, err
}

type pendingDeletion struct {
	scope  database.DeleteScope
	counts database.DeleteCounts
	label  string
}

// SettingsPage is the settings page model. It owns no configuration of its own:
// the loaded contract.Config is the single source of truth, and every edit is
// validated and written by the store before it becomes visible here.
type SettingsPage struct {
	mu         sync.Mutex
	store      SettingsStore
	data       SettingsDataSource
	config     contract.Config
	connectors []ConnectorSettings
	pending    *pendingDeletion
	status     string
}

// NewSettingsPage builds a page over a store and a data source.
func NewSettingsPage(store SettingsStore, data SettingsDataSource) *SettingsPage {
	return &SettingsPage{store: store, data: data}
}

// Load reads the configuration and the connector list. A failed load keeps the
// page usable but reports the reason.
func (p *SettingsPage) Load(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	config, err := p.store.Load()
	if err != nil {
		p.status = "could not load the settings: " + err.Error()
		return err
	}
	if p.data != nil {
		connectors, err := p.data.Connectors(ctx)
		if err != nil {
			p.status = "could not list the connectors: " + err.Error()
			return err
		}
		p.connectors = connectors
	}
	p.config = config
	p.pending = nil
	p.applyConnectorState()
	p.status = "settings loaded"
	return nil
}

// Config returns the current configuration (a copy; callers cannot mutate it).
func (p *SettingsPage) Config() contract.Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneConfig(p.config)
}

// Connectors returns the connector list with the enablement applied.
func (p *SettingsPage) Connectors() []ConnectorSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ConnectorSettings(nil), p.connectors...)
}

// Status returns the last human-readable result.
func (p *SettingsPage) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// AllowsCapture is the immediate effect of the page: false as soon as the
// connector, the project or the global switch is off, without a reload.
func (p *SettingsPage) AllowsCapture(connectorName, projectKey string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.allowsCaptureLocked(connectorName, projectKey)
}

func (p *SettingsPage) allowsCaptureLocked(connectorName, projectKey string) bool {
	if p.config.Capture.Enabled != nil && !*p.config.Capture.Enabled {
		return false
	}
	if containsString(p.config.Capture.PausedConnectors, connectorName) {
		return false
	}
	if projectKey != "" && containsString(p.config.Capture.PausedProjects, projectKey) {
		return false
	}
	return true
}

// SetConnectorEnabled starts or stops one connector immediately. The change is
// saved before it is applied here, so a failed save leaves the page unchanged.
func (p *SettingsPage) SetConnectorEnabled(ctx context.Context, name string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if name == "" {
		return errors.New("settings: a connector name is required")
	}
	known := false
	for _, connector := range p.connectors {
		if connector.Name == name {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("settings: unknown connector %q", name)
	}
	config := cloneConfig(p.config)
	config.Capture.PausedConnectors = setPaused(config.Capture.PausedConnectors, name, !enabled)
	if err := p.commit(config); err != nil {
		return err
	}
	p.applyConnectorState()
	if enabled {
		p.status = fmt.Sprintf("capture for %s is on", name)
	} else {
		p.status = fmt.Sprintf("capture for %s is off (no new writes)", name)
	}
	return nil
}

// SetCaptureEnabled is the global pause switch. Pausing also turns notices off,
// because contract.Config rejects a notice while capture is off; resuming turns
// them back on.
func (p *SettingsPage) SetCaptureEnabled(ctx context.Context, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	config := cloneConfig(p.config)
	config.Capture.Enabled = boolPointer(enabled)
	config.Notices.BeforeDiagnosis = boolPointer(enabled)
	if err := p.commit(config); err != nil {
		return err
	}
	if enabled {
		p.status = "capture is on"
	} else {
		p.status = "capture is paused; nothing is written and nothing is injected"
	}
	return nil
}

// CaptureEnabled reports the global switch.
func (p *SettingsPage) CaptureEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config.Capture.Enabled == nil || *p.config.Capture.Enabled
}

// SetProjectEnabled pauses or resumes capture for one project.
func (p *SettingsPage) SetProjectEnabled(ctx context.Context, projectKey string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if projectKey == "" {
		return errors.New("settings: a project key is required")
	}
	config := cloneConfig(p.config)
	config.Capture.PausedProjects = setPaused(config.Capture.PausedProjects, projectKey, !enabled)
	if err := p.commit(config); err != nil {
		return err
	}
	if enabled {
		p.status = fmt.Sprintf("capture for project %s is on", projectKey)
	} else {
		p.status = fmt.Sprintf("capture for project %s is off", projectKey)
	}
	return nil
}

// PausedProjects returns the paused project keys.
func (p *SettingsPage) PausedProjects() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.config.Capture.PausedProjects...)
}

// RetentionDays returns the three independent retention periods.
func (p *SettingsPage) RetentionDays() (samples, normalized, audit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return intValue(p.config.Retention.SampleDays), intValue(p.config.Retention.NormalizedDays),
		intValue(p.config.Retention.AuditDays)
}

// SetRetention changes the three periods independently.
func (p *SettingsPage) SetRetention(ctx context.Context, samples, normalized, audit int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if samples < 0 || normalized < 0 || audit < 0 {
		return errors.New("settings: retention days must be zero or positive")
	}
	config := cloneConfig(p.config)
	config.Retention.SampleDays = intPointer(samples)
	config.Retention.NormalizedDays = intPointer(normalized)
	config.Retention.AuditDays = intPointer(audit)
	if err := p.commit(config); err != nil {
		return err
	}
	p.status = fmt.Sprintf("retention is %d / %d / %d days (samples / normalized / audit)",
		samples, normalized, audit)
	return nil
}

// HTTPState reports the loopback listener settings.
func (p *SettingsPage) HTTPState() (enabled bool, host string, token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config.Service.HTTP.Enabled != nil && *p.config.Service.HTTP.Enabled,
		p.config.Service.HTTP.Host, p.config.Service.HTTP.Token
}

// PipeState reports the default transport: the Windows Named Pipe is on unless
// the configuration says otherwise.
func (p *SettingsPage) PipeState() (enabled bool, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config.Service.Pipe.Enabled == nil || *p.config.Service.Pipe.Enabled,
		p.config.Service.Pipe.Name
}

// SetHTTP turns the loopback listener on or off. Turning it on without a token
// is refused before anything is written, and contract.Config enforces the token
// length and the loopback-only host on save.
func (p *SettingsPage) SetHTTP(ctx context.Context, enabled bool, token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if enabled && strings.TrimSpace(token) == "" {
		return errors.New("settings: a token is required before the loopback HTTP listener can be enabled")
	}
	config := cloneConfig(p.config)
	config.Service.HTTP.Enabled = boolPointer(enabled)
	if token != "" {
		config.Service.HTTP.Token = token
	}
	if err := p.commit(config); err != nil {
		return err
	}
	if enabled {
		p.status = "loopback HTTP is on (token required, loopback only)"
	} else {
		p.status = "loopback HTTP is off"
	}
	return nil
}

// PreviewDelete counts the rows a scoped deletion would remove and remembers the
// plan. Nothing is removed until ConfirmDelete.
func (p *SettingsPage) PreviewDelete(ctx context.Context, request DeleteRequest) (database.DeleteCounts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if request.ProjectKey == "" && request.ConnectorID == "" && request.Before == nil {
		return database.DeleteCounts{},
			errors.New("settings: a deletion needs a project, a connector or a time")
	}
	if p.data == nil {
		return database.DeleteCounts{}, errors.New("settings: the database is not available")
	}
	scope := database.DeleteScope{
		ProjectKey:  request.ProjectKey,
		ConnectorID: request.ConnectorID,
		Before:      request.Before,
		Actor:       SettingsActor,
	}
	counts, err := p.data.PreviewDelete(ctx, scope)
	if err != nil {
		p.status = "could not count what would be deleted: " + err.Error()
		return database.DeleteCounts{}, err
	}
	p.pending = &pendingDeletion{scope: scope, counts: counts, label: describeDeleteScope(request)}
	if totalDeleteCounts(counts) == 0 {
		p.status = "nothing matches; there is nothing to delete"
	} else {
		p.status = "review the counts, then confirm: " + FormatDeleteCounts(counts)
	}
	return counts, nil
}

// PendingDeletion returns the previewed counts and whether a deletion is
// waiting for confirmation.
func (p *SettingsPage) PendingDeletion() (database.DeleteCounts, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		return database.DeleteCounts{}, false
	}
	return p.pending.counts, true
}

// ConfirmDelete performs the previewed deletion. It refuses to run without a
// preview, so a destructive click always follows visible numbers.
func (p *SettingsPage) ConfirmDelete(ctx context.Context) (database.DeleteCounts, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		return database.DeleteCounts{}, errors.New("settings: nothing was previewed; preview the deletion before confirming it")
	}
	plan := *p.pending
	deleted, err := p.data.DeleteScoped(ctx, plan.scope)
	if err != nil {
		p.status = "deletion failed: " + err.Error()
		return database.DeleteCounts{}, err
	}
	p.pending = nil
	if !sameDeleteCounts(plan.counts, deleted) {
		p.status = fmt.Sprintf("deleted %s; the preview differed (%s) because the data changed meanwhile — review again",
			FormatDeleteCounts(deleted), FormatCountsDelta(plan.counts, deleted))
	} else {
		p.status = "deleted exactly what the preview showed: " + FormatDeleteCounts(deleted)
	}
	return deleted, nil
}

// CancelDelete drops the previewed deletion without touching the database.
func (p *SettingsPage) CancelDelete() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = nil
	p.status = "deletion cancelled; nothing was removed"
}

// ApplyRetention runs one retention pass with the configured periods. It is
// destructive, so it requires an explicit confirmation.
func (p *SettingsPage) ApplyRetention(ctx context.Context, confirmed bool) (database.RetentionReport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !confirmed {
		return database.RetentionReport{}, errors.New("settings: the retention pass removes data; confirm it explicitly")
	}
	if p.data == nil {
		return database.RetentionReport{}, errors.New("settings: the database is not available")
	}
	if p.config.Retention.SampleDays == nil || p.config.Retention.NormalizedDays == nil ||
		p.config.Retention.AuditDays == nil {
		return database.RetentionReport{}, errors.New("settings: the retention periods are not loaded")
	}
	policy := database.RetentionPolicy{
		SampleDays:     *p.config.Retention.SampleDays,
		NormalizedDays: *p.config.Retention.NormalizedDays,
		AuditDays:      *p.config.Retention.AuditDays,
	}
	report, err := p.data.ApplyRetention(ctx, policy, time.Now())
	if err != nil {
		p.status = "the retention pass failed: " + err.Error()
		return database.RetentionReport{}, err
	}
	p.status = fmt.Sprintf("retention removed %d sample(s), %d event(s) and %d audit row(s)",
		report.SamplesCleared, report.EventsRemoved, report.AuditRowsRemoved)
	return report, nil
}

// commit validates a candidate configuration and saves it, and only then makes
// it the page's configuration. The validation runs here and again in the store,
// so an invalid document can never be persisted by any store implementation.
func (p *SettingsPage) commit(config contract.Config) error {
	if err := config.Validate(); err != nil {
		p.status = "not saved: " + err.Error()
		return err
	}
	if err := p.store.Save(config); err != nil {
		p.status = "not saved: " + err.Error()
		return err
	}
	p.config = config
	return nil
}

func (p *SettingsPage) applyConnectorState() {
	for index := range p.connectors {
		p.connectors[index].Enabled = p.allowsCaptureLocked(p.connectors[index].Name, "")
	}
}

// FormatDeleteCounts renders the preview in the language of the plan: events,
// occurrences and the derived rows.
func FormatDeleteCounts(counts database.DeleteCounts) string {
	parts := []string{
		fmt.Sprintf("%d occurrence(s)", counts.Occurrences),
		fmt.Sprintf("%d event(s)", counts.Events),
		fmt.Sprintf("%d issue(s)", counts.Issues),
		fmt.Sprintf("%d fix version(s)", counts.SolutionVersions),
		fmt.Sprintf("%d outcome(s)", counts.Outcomes),
		fmt.Sprintf("%d retrieval(s)", counts.Retrievals),
		fmt.Sprintf("%d notice(s)", counts.Notices),
		fmt.Sprintf("%d prevention artifact(s)", counts.PreventionArtifacts),
		fmt.Sprintf("%d prevention version(s)", counts.PreventionArtifactVersions),
		fmt.Sprintf("%d prevention link(s)", counts.IssuePreventions),
		fmt.Sprintf("%d prevention verification(s)", counts.PreventionVerifications),
	}
	return strings.Join(parts, ", ")
}

// FormatCountsDelta describes how the actual counts deviated from the preview.
func FormatCountsDelta(preview, actual database.DeleteCounts) string {
	return fmt.Sprintf("events %d→%d, occurrences %d→%d, issues %d→%d",
		preview.Events, actual.Events, preview.Occurrences, actual.Occurrences,
		preview.Issues, actual.Issues)
}

func describeDeleteScope(request DeleteRequest) string {
	parts := make([]string, 0, 3)
	if request.ProjectKey != "" {
		parts = append(parts, "project "+request.ProjectKey)
	}
	if request.ConnectorID != "" {
		parts = append(parts, "connector "+request.ConnectorID)
	}
	if request.Before != nil {
		parts = append(parts, "before "+request.Before.Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, ", ")
}

func cloneConfig(config contract.Config) contract.Config {
	copied := config
	copied.Capture.PausedConnectors = append([]string(nil), config.Capture.PausedConnectors...)
	copied.Capture.PausedProjects = append([]string(nil), config.Capture.PausedProjects...)
	return copied
}

func setPaused(paused []string, name string, pause bool) []string {
	result := make([]string, 0, len(paused)+1)
	found := false
	for _, existing := range paused {
		if existing == name {
			found = true
			if pause {
				result = append(result, existing)
			}
			continue
		}
		result = append(result, existing)
	}
	if pause && !found {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func boolPointer(value bool) *bool { return &value }

func intPointer(value int) *int { return &value }

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func totalDeleteCounts(counts database.DeleteCounts) int64 {
	return counts.Issues + counts.Solutions + counts.SolutionVersions + counts.Outcomes +
		counts.Retrievals + counts.Notices + counts.Occurrences + counts.Events +
		counts.PreventionArtifacts + counts.PreventionArtifactVersions +
		counts.IssuePreventions + counts.PreventionVerifications
}

func sameDeleteCounts(a, b database.DeleteCounts) bool { return a == b }
