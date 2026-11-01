package ui_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/ui"
)

const settingsConfigYAML = `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
`

func settingsConfig(t *testing.T) contract.Config {
	t.Helper()
	config, err := contract.LoadConfig([]byte(settingsConfigYAML))
	if err != nil {
		t.Fatalf("load the base configuration: %v", err)
	}
	return config
}

type fakeSettingsStore struct {
	mu      sync.Mutex
	config  contract.Config
	loadErr error
	saveErr error
	saves   int
}

func (s *fakeSettingsStore) Load() (contract.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return contract.Config{}, s.loadErr
	}
	return s.config, nil
}

func (s *fakeSettingsStore) Save(config contract.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.config = config
	s.saves++
	return nil
}

type fakeSettingsData struct {
	connectors    []ui.ConnectorSettings
	connectorsErr error
	preview       database.DeleteCounts
	previewErr    error
	deleted       database.DeleteCounts
	deleteErr     error
	report        database.RetentionReport
	retentionErr  error
	scopes        []database.DeleteScope
	policies      []database.RetentionPolicy
}

func (f *fakeSettingsData) Connectors(context.Context) ([]ui.ConnectorSettings, error) {
	return append([]ui.ConnectorSettings(nil), f.connectors...), f.connectorsErr
}

func (f *fakeSettingsData) PreviewDelete(_ context.Context, scope database.DeleteScope) (database.DeleteCounts, error) {
	f.scopes = append(f.scopes, scope)
	return f.preview, f.previewErr
}

func (f *fakeSettingsData) DeleteScoped(_ context.Context, scope database.DeleteScope) (database.DeleteCounts, error) {
	f.scopes = append(f.scopes, scope)
	return f.deleted, f.deleteErr
}

func (f *fakeSettingsData) ApplyRetention(_ context.Context, policy database.RetentionPolicy, _ time.Time) (database.RetentionReport, error) {
	f.policies = append(f.policies, policy)
	return f.report, f.retentionErr
}

func newSettingsPage(t *testing.T, saveErr error) (*ui.SettingsPage, *fakeSettingsStore, *fakeSettingsData) {
	t.Helper()
	store := &fakeSettingsStore{config: settingsConfig(t), saveErr: saveErr}
	data := &fakeSettingsData{
		connectors: []ui.ConnectorSettings{
			{ID: "connector-1", Name: "dsh", Channel: "A", Version: "0.2.0-rc.2"},
			{ID: "connector-2", Name: "codex-cli", Channel: "B", Version: "0.156.1"},
		},
	}
	page := ui.NewSettingsPage(store, data)
	if err := page.Load(context.Background()); err != nil {
		t.Fatalf("load the settings page: %v", err)
	}
	return page, store, data
}

func TestFileSettingsStoreRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "errkb.yaml")
	store := ui.FileSettingsStore{Path: path}
	base := settingsConfig(t)
	if err := store.Save(base); err != nil {
		t.Fatalf("save the base configuration: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load the saved configuration: %v", err)
	}
	if loaded.Database.DSN != base.Database.DSN {
		t.Errorf("DSN = %q, want %q", loaded.Database.DSN, base.Database.DSN)
	}
	if samples, normalized, audit := intValue(loaded.Retention.SampleDays),
		intValue(loaded.Retention.NormalizedDays), intValue(loaded.Retention.AuditDays); samples != 7 || normalized != 90 || audit != 365 {
		t.Errorf("retention = %d/%d/%d, want 7/90/365", samples, normalized, audit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the saved file: %v", err)
	}
	if _, err := contract.LoadConfig(data); err != nil {
		t.Fatalf("the saved file is not a valid configuration: %v", err)
	}
}

func TestFileSettingsStoreRefusesInvalidConfiguration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "errkb.yaml")
	store := ui.FileSettingsStore{Path: path}
	base := settingsConfig(t)
	if err := store.Save(base); err != nil {
		t.Fatalf("save the base configuration: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the file: %v", err)
	}
	invalid := base
	invalid.Capture.Enabled = boolPtr(false)
	invalid.Notices.BeforeDiagnosis = boolPtr(true) // contract.Config rejects this pair
	if err := store.Save(invalid); err == nil {
		t.Fatal("saving a configuration that contract.LoadConfig rejects must fail")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the file after the failed save: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("a failed save must leave the file untouched")
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("the original file must still load: %v", err)
	}
}

func TestSettingsLoadReportsBrokenFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "errkb.yaml")
	if err := os.WriteFile(path, []byte("database: [\n"), 0o600); err != nil {
		t.Fatalf("write the broken file: %v", err)
	}
	page := ui.NewSettingsPage(ui.FileSettingsStore{Path: path}, nil)
	if err := page.Load(context.Background()); err == nil {
		t.Fatal("loading a broken configuration must fail")
	}
	if !strings.Contains(page.Status(), "could not load") {
		t.Errorf("status = %q, want a readable load reason", page.Status())
	}
}

func TestConnectorToggleStopsCaptureImmediately(t *testing.T) {
	t.Parallel()
	page, store, _ := newSettingsPage(t, nil)
	ctx := context.Background()
	if !page.AllowsCapture("dsh", "default") {
		t.Fatal("capture must start enabled")
	}
	if err := page.SetConnectorEnabled(ctx, "dsh", false); err != nil {
		t.Fatalf("pause the connector: %v", err)
	}
	if page.AllowsCapture("dsh", "default") {
		t.Fatal("pausing a connector must stop its captures immediately")
	}
	if page.AllowsCapture("codex-cli", "default") != true {
		t.Fatal("pausing one connector must not stop the others")
	}
	if store.saves != 1 {
		t.Errorf("saves = %d, want 1", store.saves)
	}
	enabled := page.Connectors()
	for _, connector := range enabled {
		if connector.Name == "dsh" && connector.Enabled {
			t.Fatal("the connector list must show dsh as disabled")
		}
	}
	if err := page.SetConnectorEnabled(ctx, "dsh", true); err != nil {
		t.Fatalf("resume the connector: %v", err)
	}
	if !page.AllowsCapture("dsh", "default") {
		t.Fatal("resuming a connector must allow captures again")
	}
	if err := page.SetConnectorEnabled(ctx, "unknown", false); err == nil {
		t.Fatal("an unknown connector must be refused")
	}
}

func TestConnectorToggleRollsBackWhenSaveFails(t *testing.T) {
	t.Parallel()
	page, _, _ := newSettingsPage(t, errors.New("disk is read-only"))
	if err := page.SetConnectorEnabled(context.Background(), "dsh", false); err == nil {
		t.Fatal("a failed save must be reported")
	}
	if !page.AllowsCapture("dsh", "default") {
		t.Fatal("a failed save must not change the effective settings")
	}
	if !strings.Contains(page.Status(), "not saved") {
		t.Errorf("status = %q, want the save failure", page.Status())
	}
}

func TestPauseStopsEverythingAndKeepsTheConfigurationValid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "errkb.yaml")
	if err := os.WriteFile(path, []byte(settingsConfigYAML), 0o600); err != nil {
		t.Fatalf("write the configuration: %v", err)
	}
	store := ui.FileSettingsStore{Path: path}
	data := &fakeSettingsData{}
	page := ui.NewSettingsPage(store, data)
	ctx := context.Background()
	if err := page.Load(ctx); err != nil {
		t.Fatalf("load the settings page: %v", err)
	}
	if err := page.SetCaptureEnabled(ctx, false); err != nil {
		t.Fatalf("pause capture: %v", err)
	}
	if page.AllowsCapture("dsh", "proj-a") {
		t.Fatal("the global pause must stop every capture")
	}
	if page.CaptureEnabled() {
		t.Fatal("CaptureEnabled must report the pause")
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the saved file: %v", err)
	}
	loaded, err := contract.LoadConfig(file)
	if err != nil {
		t.Fatalf("a pause must leave a configuration that contract.LoadConfig accepts: %v", err)
	}
	if loaded.Notices.BeforeDiagnosis == nil || *loaded.Notices.BeforeDiagnosis {
		t.Fatal("pausing must also turn notices off, so the saved configuration stays valid")
	}
	if err := page.SetCaptureEnabled(ctx, true); err != nil {
		t.Fatalf("resume capture: %v", err)
	}
	if !page.AllowsCapture("dsh", "proj-a") {
		t.Fatal("resuming must allow captures again")
	}
	if !*page.Config().Notices.BeforeDiagnosis {
		t.Fatal("resuming must turn notices back on")
	}
}

func TestProjectScopeIsIndependent(t *testing.T) {
	t.Parallel()
	page, _, _ := newSettingsPage(t, nil)
	ctx := context.Background()
	if err := page.SetProjectEnabled(ctx, "proj-a", false); err != nil {
		t.Fatalf("pause project: %v", err)
	}
	if page.AllowsCapture("dsh", "proj-a") {
		t.Fatal("a paused project must not be captured")
	}
	if !page.AllowsCapture("dsh", "proj-b") {
		t.Fatal("other projects must keep capturing")
	}
	if paused := page.PausedProjects(); len(paused) != 1 || paused[0] != "proj-a" {
		t.Errorf("paused projects = %v, want [proj-a]", paused)
	}
	if err := page.SetProjectEnabled(ctx, "proj-a", true); err != nil {
		t.Fatalf("resume project: %v", err)
	}
	if err := page.SetProjectEnabled(ctx, "", false); err == nil {
		t.Fatal("an empty project key must be refused")
	}
}

func TestRetentionIsThreeIndependentPeriods(t *testing.T) {
	t.Parallel()
	page, store, _ := newSettingsPage(t, nil)
	ctx := context.Background()
	if samples, normalized, audit := page.RetentionDays(); samples != 7 || normalized != 90 || audit != 365 {
		t.Fatalf("retention = %d/%d/%d, want 7/90/365", samples, normalized, audit)
	}
	if err := page.SetRetention(ctx, 3, 30, 120); err != nil {
		t.Fatalf("change retention: %v", err)
	}
	if samples, normalized, audit := page.RetentionDays(); samples != 3 || normalized != 30 || audit != 120 {
		t.Fatalf("retention = %d/%d/%d, want 3/30/120", samples, normalized, audit)
	}
	if store.saves != 1 {
		t.Errorf("saves = %d, want 1", store.saves)
	}
	if err := page.SetRetention(ctx, -1, 30, 120); err == nil {
		t.Fatal("negative retention days must be refused")
	}
}

func TestHTTPToggleRequiresAToken(t *testing.T) {
	t.Parallel()
	page, _, _ := newSettingsPage(t, nil)
	ctx := context.Background()
	if enabled, _, _ := page.HTTPState(); enabled {
		t.Fatal("the loopback HTTP listener must start off")
	}
	if err := page.SetHTTP(ctx, true, ""); err == nil {
		t.Fatal("enabling the listener without a token must be refused")
	}
	if err := page.SetHTTP(ctx, true, "short"); err == nil {
		t.Fatal("enabling the listener with a token shorter than 32 characters must be refused")
	}
	token := strings.Repeat("t", 32)
	if err := page.SetHTTP(ctx, true, token); err != nil {
		t.Fatalf("enable the listener with a valid token: %v", err)
	}
	enabled, host, stored := page.HTTPState()
	if !enabled || host != "127.0.0.1" || stored != token {
		t.Fatalf("HTTP state = %v %q %q, want enabled on 127.0.0.1", enabled, host, stored)
	}
}

func TestDeletePreviewThenConfirm(t *testing.T) {
	t.Parallel()
	page, _, data := newSettingsPage(t, nil)
	ctx := context.Background()
	data.preview = database.DeleteCounts{Issues: 2, Occurrences: 5, Events: 5}
	data.deleted = data.preview
	if _, err := page.PreviewDelete(ctx, ui.DeleteRequest{}); err == nil {
		t.Fatal("a deletion without a project, connector or time must be refused")
	}
	counts, err := page.PreviewDelete(ctx, ui.DeleteRequest{ProjectKey: "proj-a"})
	if err != nil {
		t.Fatalf("preview the deletion: %v", err)
	}
	if counts.Events != 5 || counts.Issues != 2 {
		t.Fatalf("preview = %+v, want the data source counts", counts)
	}
	if pending, ok := page.PendingDeletion(); !ok || pending.Events != 5 {
		t.Fatal("the preview must be remembered until it is confirmed or cancelled")
	}
	if !strings.Contains(page.Status(), "confirm") {
		t.Errorf("status = %q, want a confirmation prompt with the counts", page.Status())
	}
	deleted, err := page.ConfirmDelete(ctx)
	if err != nil {
		t.Fatalf("confirm the deletion: %v", err)
	}
	if deleted != data.deleted {
		t.Fatalf("deleted = %+v, want %+v", deleted, data.deleted)
	}
	if pending, ok := page.PendingDeletion(); ok || pending != (database.DeleteCounts{}) {
		t.Fatal("a confirmed deletion must clear the pending plan")
	}
	if len(data.scopes) != 2 || data.scopes[0].Actor != ui.SettingsActor {
		t.Fatalf("scopes = %+v, want preview + delete with actor %q", data.scopes, ui.SettingsActor)
	}
	if !strings.Contains(page.Status(), "deleted exactly") {
		t.Errorf("status = %q, want the matching-count confirmation", page.Status())
	}
}

func TestConfirmWithoutPreviewIsRefused(t *testing.T) {
	t.Parallel()
	page, _, _ := newSettingsPage(t, nil)
	if _, err := page.ConfirmDelete(context.Background()); err == nil {
		t.Fatal("confirming a deletion that was never previewed must be refused")
	}
}

func TestDeletePreviewDriftIsReported(t *testing.T) {
	t.Parallel()
	page, _, data := newSettingsPage(t, nil)
	ctx := context.Background()
	data.preview = database.DeleteCounts{Events: 3, Occurrences: 3, Issues: 1}
	data.deleted = database.DeleteCounts{Events: 2, Occurrences: 2, Issues: 1}
	if _, err := page.PreviewDelete(ctx, ui.DeleteRequest{ConnectorID: "connector-1"}); err != nil {
		t.Fatalf("preview the deletion: %v", err)
	}
	if _, err := page.ConfirmDelete(ctx); err != nil {
		t.Fatalf("confirm the deletion: %v", err)
	}
	if !strings.Contains(page.Status(), "preview differed") {
		t.Errorf("status = %q, want a warning that the preview changed", page.Status())
	}
}

func TestCancelDeleteRemovesNothing(t *testing.T) {
	t.Parallel()
	page, _, data := newSettingsPage(t, nil)
	ctx := context.Background()
	data.preview = database.DeleteCounts{Events: 1}
	if _, err := page.PreviewDelete(ctx, ui.DeleteRequest{ProjectKey: "proj-a"}); err != nil {
		t.Fatalf("preview the deletion: %v", err)
	}
	page.CancelDelete()
	if _, ok := page.PendingDeletion(); ok {
		t.Fatal("cancel must drop the pending deletion")
	}
	if len(data.scopes) != 1 {
		t.Fatalf("cancel must not call the deleting data source; scopes = %+v", data.scopes)
	}
}

func TestRetentionPassNeedsConfirmation(t *testing.T) {
	t.Parallel()
	page, _, data := newSettingsPage(t, nil)
	ctx := context.Background()
	data.report = database.RetentionReport{SamplesCleared: 4, EventsRemoved: 2, AuditRowsRemoved: 1}
	if _, err := page.ApplyRetention(ctx, false); err == nil {
		t.Fatal("a retention pass without confirmation must be refused")
	}
	if len(data.policies) != 0 {
		t.Fatal("a refused retention pass must not reach the data source")
	}
	report, err := page.ApplyRetention(ctx, true)
	if err != nil {
		t.Fatalf("run the retention pass: %v", err)
	}
	if report.SamplesCleared != 4 || report.EventsRemoved != 2 {
		t.Fatalf("report = %+v, want the data source report", report)
	}
	if len(data.policies) != 1 || data.policies[0] != (database.RetentionPolicy{SampleDays: 7, NormalizedDays: 90, AuditDays: 365}) {
		t.Fatalf("policies = %+v, want the configured periods", data.policies)
	}
	if !strings.Contains(page.Status(), "removed") {
		t.Errorf("status = %q, want the removal counts", page.Status())
	}
}

func TestFormatDeleteCounts(t *testing.T) {
	t.Parallel()
	counts := database.DeleteCounts{
		Issues: 2, Occurrences: 5, Events: 5, SolutionVersions: 1,
		PreventionArtifacts: 2, PreventionArtifactVersions: 3,
		IssuePreventions: 4, PreventionVerifications: 6,
	}
	text := ui.FormatDeleteCounts(counts)
	for _, want := range []string{
		"5 occurrence(s)", "5 event(s)", "2 issue(s)", "1 fix version(s)",
		"2 prevention artifact(s)", "3 prevention version(s)",
		"4 prevention link(s)", "6 prevention verification(s)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatDeleteCounts = %q, missing %q", text, want)
		}
	}
}

func TestDeletePreviewWithOnlyPreventionCountsNeedsConfirmation(t *testing.T) {
	t.Parallel()
	page, _, data := newSettingsPage(t, nil)
	data.preview = database.DeleteCounts{PreventionArtifacts: 1, PreventionArtifactVersions: 1}
	if _, err := page.PreviewDelete(context.Background(), ui.DeleteRequest{ProjectKey: "proj-a"}); err != nil {
		t.Fatalf("preview prevention-only deletion: %v", err)
	}
	if !strings.Contains(page.Status(), "review the counts") {
		t.Fatalf("status = %q, want a confirmation prompt for prevention rows", page.Status())
	}
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func boolPtr(value bool) *bool { return &value }

// TestPostgresSettingsAgainstDatabase exercises the real data source: the
// connector list with channel and last activity, the scoped deletion with its
// audit row, and the three independent retention classes.
func TestPostgresSettingsAgainstDatabase(t *testing.T) {
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ERRKB_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("errkb_settings_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer func() { _, _ = admin.Exec(ctx, "drop schema "+schema+" cascade") }()

	conn := connectInSchema(t, ctx, dsn, schema)
	defer func() { _ = conn.Close(ctx) }()

	connector, err := database.EnsureConnector(ctx, conn, "settings-db", "B", "1.0")
	if err != nil {
		t.Fatalf("create the connector: %v", err)
	}
	sessionA, err := database.EnsureSession(ctx, conn, connector, "settings-session-a", "proj-settings-a")
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sessionB, err := database.EnsureSession(ctx, conn, connector, "settings-session-b", "proj-settings-b")
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	recordSettingsEvent(t, ctx, conn, connector, sessionA, "proj-settings-a", "error: build failed", time.Now())
	recordSettingsEvent(t, ctx, conn, connector, sessionB, "proj-settings-b", "error: tests failed", time.Now())

	// Retention seeds: one event inside the normalized window (sample cleared),
	// one event older than the normalized window (removed entirely).
	recordSettingsEvent(t, ctx, conn, connector, sessionA, "proj-settings-c", "error: link failed",
		time.Now().Add(-10*24*time.Hour))
	recordSettingsEvent(t, ctx, conn, connector, sessionA, "proj-settings-c", "error: archive expired",
		time.Now().Add(-400*24*time.Hour))

	pool := poolInSchema(t, ctx, dsn, schema)
	defer pool.Close()

	data := &ui.PostgresSettings{Pool: pool}
	connectors, err := data.Connectors(ctx)
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	if len(connectors) != 1 || connectors[0].Name != "settings-db" || connectors[0].Channel != "B" {
		t.Fatalf("connectors = %+v, want one B connector", connectors)
	}
	if connectors[0].LastActive.IsZero() {
		t.Fatal("the connector list must carry the last activity time")
	}

	preview, err := data.PreviewDelete(ctx, database.DeleteScope{ProjectKey: "proj-settings-a", Actor: ui.SettingsActor})
	if err != nil {
		t.Fatalf("preview the deletion: %v", err)
	}
	if preview.Events != 1 || preview.Occurrences != 1 || preview.Issues != 1 {
		t.Fatalf("preview = %+v, want the single project-a event", preview)
	}
	deleted, err := data.DeleteScoped(ctx, database.DeleteScope{ProjectKey: "proj-settings-a", Actor: ui.SettingsActor})
	if err != nil {
		t.Fatalf("delete the project: %v", err)
	}
	if deleted != preview {
		t.Fatalf("deleted = %+v, want the preview %+v", deleted, preview)
	}
	var remaining int
	if err := conn.QueryRow(ctx, "select count(*) from events where project_key = 'proj-settings-b'").Scan(&remaining); err != nil {
		t.Fatalf("count the surviving project: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("project-b events = %d, want 1 after deleting project-a only", remaining)
	}
	var audits int
	if err := conn.QueryRow(ctx, "select count(*) from audit_log where action = 'delete.scope'").Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 1 {
		t.Fatalf("audit rows = %d, want one delete.scope row", audits)
	}

	report, err := data.ApplyRetention(ctx, database.RetentionPolicy{SampleDays: 7, NormalizedDays: 90, AuditDays: 365}, time.Now())
	if err != nil {
		t.Fatalf("run retention: %v", err)
	}
	if report.SamplesCleared < 1 {
		t.Errorf("samples cleared = %d, want the 10-day-old event's sample cleared", report.SamplesCleared)
	}
	if report.EventsRemoved < 1 {
		t.Errorf("events removed = %d, want the 400-day-old event removed", report.EventsRemoved)
	}
	var sampled, total int
	if err := conn.QueryRow(ctx, `
		select count(*) filter (where redacted_sample = ''), count(*)
		from events where project_key = 'proj-settings-c'
	`).Scan(&sampled, &total); err != nil {
		t.Fatalf("count retention effects: %v", err)
	}
	if total != 1 || sampled != 1 {
		t.Fatalf("retention left %d events in project-c (%d without samples), want the expired one removed and the sample cleared", total, sampled)
	}
}

func connectInSchema(t *testing.T, ctx context.Context, dsn, schema string) *pgx.Conn {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse the DSN: %v", err)
	}
	config.RuntimeParams["search_path"] = schema + ",public"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect in schema %s: %v", schema, err)
	}
	if _, err := database.Migrate(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatalf("migrate %s: %v", schema, err)
	}
	return conn
}

func poolInSchema(t *testing.T, ctx context.Context, dsn, schema string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse the DSN for the pool: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+schema+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create the pool: %v", err)
	}
	return pool
}

func recordSettingsEvent(t *testing.T, ctx context.Context, conn *pgx.Conn, connector, session, project, message string, at time.Time) {
	t.Helper()
	normalized := core.Normalize(message)
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           fmt.Sprintf("00000000-0000-4000-8000-%012d", eventCounter.Add(1)),
		ConnectorID:       connector,
		SessionID:         session,
		ObservedAt:        at.UTC().Format(time.RFC3339),
		Channel:           "B",
		Category:          "command-exit",
		Severity:          "error",
		Transient:         boolPtr(false),
		Headline:          message,
		NormalizedMessage: normalized,
		RedactedSample:    message,
		OS:                "windows",
		ProjectKey:        project,
		Fingerprint:       core.Signature("command-exit", normalized+project),
		Evidence:          "settings test",
		Confidence:        floatPtr(0.5),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("build a valid event: %v", err)
	}
	if _, err := database.RecordEvent(ctx, conn, event); err != nil {
		t.Fatalf("record the event: %v", err)
	}
}

type settingsEventCounter struct {
	mu sync.Mutex
	n  int64
}

func (c *settingsEventCounter) Add(delta int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += delta
	return c.n
}

var eventCounter settingsEventCounter

func floatPtr(value float64) *float64 { return &value }
