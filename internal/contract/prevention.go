package contract

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const PreventionSchemaVersion = "v1"

const (
	OperationPreventionAdd      = "prevention_add"
	OperationPreventionVerify   = "prevention_verify"
	OperationPreventionActivate = "prevention_activate"
	OperationPreventionList     = "prevention_list"
	OperationPreventionGet      = "prevention_get"
	OperationPreventionStats    = "prevention_stats"
)

var (
	preventionSHA256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	preventionCommitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	validPreventionKinds      = map[string]bool{"regression_test": true, "deterministic_check": true, "agent_instruction": true, "intent": true}
	validPreventionStatuses   = map[string]bool{"draft": true, "verified": true, "active": true, "deprecated": true}
	validObservationStatuses  = map[string]bool{"observing": true, "mature": true}
	preventionEnvironmentKeys = map[string]bool{"os": true, "shell": true, "go_version": true, "git_version": true}
)

func requirePreventionVersion(operation, version string) error {
	if version != PreventionSchemaVersion {
		return fmt.Errorf("%s.schema_version: want %q, got %q", operation, PreventionSchemaVersion, version)
	}
	return nil
}

func validatePreventionPath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) {
		return fmt.Errorf("artifact_path: want a non-empty repository-relative slash path, got %q", path)
	}
	if len(path) >= 2 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' {
		return fmt.Errorf("artifact_path: absolute drive path is forbidden")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.EqualFold(segment, ".git") {
			return fmt.Errorf("artifact_path: forbidden path segment in %q", path)
		}
	}
	return nil
}

func validatePreventionTime(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s: required", field)
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

func validatePreventionKind(kind string) error {
	if !validPreventionKinds[kind] {
		return fmt.Errorf("kind: unknown prevention kind %q", kind)
	}
	return nil
}

type PreventionAddRequest struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	ArtifactPath  string `json:"artifact_path"`
	ContentSHA256 string `json:"content_sha256"`
	Source        string `json:"source,omitempty"`
	Actor         string `json:"actor,omitempty"`
}

func (r PreventionAddRequest) Validate() error {
	if err := requirePreventionVersion(OperationPreventionAdd, r.SchemaVersion); err != nil {
		return err
	}
	if r.IssueID == "" {
		return errors.New("prevention_add.issue_id: required")
	}
	if err := validatePreventionKind(r.Kind); err != nil {
		return err
	}
	if r.Title == "" {
		return errors.New("prevention_add.title: required")
	}
	if err := validatePreventionPath(r.ArtifactPath); err != nil {
		return err
	}
	if !preventionSHA256Pattern.MatchString(r.ContentSHA256) {
		return errors.New("prevention_add.content_sha256: want 64 lowercase hex characters")
	}
	for _, text := range []struct{ name, value string }{{"title", r.Title}, {"source", r.Source}, {"actor", r.Actor}} {
		if err := checkRedacted("prevention_add."+text.name, text.value); err != nil {
			return err
		}
	}
	return nil
}

type PreventionVerifyRequest struct {
	SchemaVersion     string   `json:"schema_version"`
	ArtifactVersionID string   `json:"artifact_version_id"`
	RepoPath          string   `json:"repo_path"`
	Command           []string `json:"command"`
}

func (r PreventionVerifyRequest) Validate() error {
	if err := requirePreventionVersion(OperationPreventionVerify, r.SchemaVersion); err != nil {
		return err
	}
	if r.ArtifactVersionID == "" {
		return errors.New("prevention_verify.artifact_version_id: required")
	}
	if r.RepoPath == "" {
		return errors.New("prevention_verify.repo_path: required")
	}
	if len(r.Command) == 0 {
		return errors.New("prevention_verify.command: at least one argv entry is required")
	}
	for _, arg := range r.Command {
		if strings.TrimSpace(arg) == "" {
			return errors.New("prevention_verify.command: argv entries must not be empty")
		}
	}
	return nil
}

type PreventionActivateRequest struct {
	SchemaVersion     string `json:"schema_version"`
	ArtifactVersionID string `json:"artifact_version_id"`
	RepoPath          string `json:"repo_path"`
}

func (r PreventionActivateRequest) Validate() error {
	if err := requirePreventionVersion(OperationPreventionActivate, r.SchemaVersion); err != nil {
		return err
	}
	if r.ArtifactVersionID == "" {
		return errors.New("prevention_activate.artifact_version_id: required")
	}
	if r.RepoPath == "" {
		return errors.New("prevention_activate.repo_path: required")
	}
	return nil
}

type PreventionListRequest struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id,omitempty"`
	ProjectKey    string `json:"project_key,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

func (r PreventionListRequest) Validate() error {
	if err := requirePreventionVersion(OperationPreventionList, r.SchemaVersion); err != nil {
		return err
	}
	if r.Kind != "" && !validPreventionKinds[r.Kind] {
		return fmt.Errorf("prevention_list.kind: unknown %q", r.Kind)
	}
	if r.Status != "" && !validPreventionStatuses[r.Status] {
		return fmt.Errorf("prevention_list.status: unknown %q", r.Status)
	}
	if r.Limit != nil && (*r.Limit < 1 || *r.Limit > 100) {
		return fmt.Errorf("prevention_list.limit: want 1..100, got %d", *r.Limit)
	}
	return nil
}

type PreventionGetRequest struct {
	SchemaVersion string `json:"schema_version"`
	ArtifactID    string `json:"artifact_id"`
}

func (r PreventionGetRequest) Validate() error {
	if err := requirePreventionVersion(OperationPreventionGet, r.SchemaVersion); err != nil {
		return err
	}
	if r.ArtifactID == "" {
		return errors.New("prevention_get.artifact_id: required")
	}
	return nil
}

type PreventionStatsRequest struct {
	SchemaVersion string `json:"schema_version"`
	ProjectKey    string `json:"project_key,omitempty"`
	IssueID       string `json:"issue_id,omitempty"`
}

func (r PreventionStatsRequest) Validate() error {
	return requirePreventionVersion(OperationPreventionStats, r.SchemaVersion)
}

type PreventionArtifact struct {
	ArtifactID string   `json:"artifact_id"`
	ProjectKey string   `json:"project_key"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	IssueIDs   []string `json:"issue_ids"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

func (a PreventionArtifact) Validate() error {
	if a.ArtifactID == "" || a.ProjectKey == "" {
		return errors.New("prevention.artifact: artifact_id and project_key are required")
	}
	if err := validatePreventionKind(a.Kind); err != nil {
		return err
	}
	if a.Title == "" {
		return errors.New("prevention.artifact.title: required")
	}
	if a.IssueIDs == nil {
		return errors.New("prevention.artifact.issue_ids: required array")
	}
	for _, id := range a.IssueIDs {
		if id == "" {
			return errors.New("prevention.artifact.issue_ids: empty issue id")
		}
	}
	if err := validatePreventionTime("prevention.artifact.created_at", a.CreatedAt); err != nil {
		return err
	}
	if err := validatePreventionTime("prevention.artifact.updated_at", a.UpdatedAt); err != nil {
		return err
	}
	return checkRedacted("prevention.artifact.title", a.Title)
}

type PreventionVersion struct {
	ArtifactVersionID  string                  `json:"artifact_version_id"`
	ArtifactID         string                  `json:"artifact_id"`
	Version            int                     `json:"version"`
	ArtifactPath       string                  `json:"artifact_path"`
	ContentSHA256      string                  `json:"content_sha256"`
	Status             string                  `json:"status"`
	CommitSHA          *string                 `json:"commit_sha,omitempty"`
	ActivatedAt        *string                 `json:"activated_at,omitempty"`
	CreatedAt          string                  `json:"created_at"`
	LatestVerification *PreventionVerification `json:"latest_verification,omitempty"`
}

func (v PreventionVersion) Validate() error {
	if v.ArtifactVersionID == "" || v.ArtifactID == "" {
		return errors.New("prevention.version: artifact_version_id and artifact_id are required")
	}
	if v.Version < 1 {
		return errors.New("prevention.version.version: want a positive integer")
	}
	if err := validatePreventionPath(v.ArtifactPath); err != nil {
		return err
	}
	if !preventionSHA256Pattern.MatchString(v.ContentSHA256) {
		return errors.New("prevention.version.content_sha256: want 64 lowercase hex characters")
	}
	if !validPreventionStatuses[v.Status] {
		return fmt.Errorf("prevention.version.status: unknown %q", v.Status)
	}
	if v.Status == "active" {
		if v.CommitSHA == nil || !preventionCommitPattern.MatchString(*v.CommitSHA) {
			return errors.New("prevention.version.commit_sha: active requires 40 lowercase hex characters")
		}
		if v.ActivatedAt == nil {
			return errors.New("prevention.version.activated_at: required for active")
		}
		if err := validatePreventionTime("prevention.version.activated_at", *v.ActivatedAt); err != nil {
			return err
		}
	} else if v.Status == "draft" || v.Status == "verified" {
		if v.CommitSHA != nil || v.ActivatedAt != nil {
			return errors.New("prevention.version: draft/verified cannot carry activation evidence")
		}
	}
	if err := validatePreventionTime("prevention.version.created_at", v.CreatedAt); err != nil {
		return err
	}
	if v.LatestVerification != nil {
		return v.LatestVerification.Validate()
	}
	return nil
}

type PreventionVerification struct {
	VerificationID    string            `json:"verification_id"`
	ArtifactVersionID string            `json:"artifact_version_id"`
	Command           []string          `json:"command"`
	ExitCode          *int              `json:"exit_code"`
	Passed            bool              `json:"passed"`
	DurationMS        int64             `json:"duration_ms"`
	Evidence          string            `json:"evidence"`
	Environment       map[string]string `json:"environment"`
	ContentSHA256     string            `json:"content_sha256"`
	CreatedAt         string            `json:"created_at"`
}

func (v PreventionVerification) Validate() error {
	if v.VerificationID == "" || v.ArtifactVersionID == "" {
		return errors.New("prevention.verification: verification_id and artifact_version_id are required")
	}
	if len(v.Command) == 0 {
		return errors.New("prevention.verification.command: required argv")
	}
	for _, arg := range v.Command {
		if strings.TrimSpace(arg) == "" {
			return errors.New("prevention.verification.command: empty argv entry")
		}
		if err := checkRedacted("prevention.verification.command", arg); err != nil {
			return err
		}
	}
	if v.DurationMS < 0 {
		return errors.New("prevention.verification.duration_ms: must not be negative")
	}
	if len(v.Evidence) > 8192 {
		return errors.New("prevention.verification.evidence: exceeds 8192 bytes")
	}
	if err := checkRedacted("prevention.verification.evidence", v.Evidence); err != nil {
		return err
	}
	if v.Environment == nil {
		return errors.New("prevention.verification.environment: required object")
	}
	for key := range v.Environment {
		if !preventionEnvironmentKeys[key] {
			return fmt.Errorf("prevention.verification.environment.%s: not allowed", key)
		}
	}
	if !preventionSHA256Pattern.MatchString(v.ContentSHA256) {
		return errors.New("prevention.verification.content_sha256: want 64 lowercase hex characters")
	}
	return validatePreventionTime("prevention.verification.created_at", v.CreatedAt)
}

type PreventionArtifactView struct {
	Artifact PreventionArtifact  `json:"artifact"`
	Versions []PreventionVersion `json:"versions"`
}

func (v PreventionArtifactView) Validate() error {
	if err := v.Artifact.Validate(); err != nil {
		return err
	}
	if v.Versions == nil {
		return errors.New("prevention.list.versions: required array")
	}
	for _, version := range v.Versions {
		if err := version.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PreventionAddResponse struct {
	SchemaVersion string             `json:"schema_version"`
	Artifact      PreventionArtifact `json:"artifact"`
	Version       PreventionVersion  `json:"version"`
	Created       bool               `json:"created"`
}

func (r PreventionAddResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionAdd, r.SchemaVersion); err != nil {
		return err
	}
	if err := r.Artifact.Validate(); err != nil {
		return err
	}
	return r.Version.Validate()
}

type PreventionVerifyResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Verification  PreventionVerification `json:"verification"`
	Version       PreventionVersion      `json:"version"`
}

func (r PreventionVerifyResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionVerify, r.SchemaVersion); err != nil {
		return err
	}
	if err := r.Verification.Validate(); err != nil {
		return err
	}
	return r.Version.Validate()
}

type PreventionActivateResponse struct {
	SchemaVersion string            `json:"schema_version"`
	Version       PreventionVersion `json:"version"`
	Activated     bool              `json:"activated"`
}

func (r PreventionActivateResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionActivate, r.SchemaVersion); err != nil {
		return err
	}
	return r.Version.Validate()
}

type PreventionListResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	Items         []PreventionArtifactView `json:"items"`
	NextCursor    string                   `json:"next_cursor,omitempty"`
}

func (r PreventionListResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionList, r.SchemaVersion); err != nil {
		return err
	}
	if r.Items == nil {
		return errors.New("prevention_list.items: required array")
	}
	for _, item := range r.Items {
		if err := item.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PreventionGetResponse struct {
	SchemaVersion string                   `json:"schema_version"`
	Artifact      PreventionArtifact       `json:"artifact"`
	Versions      []PreventionVersion      `json:"versions"`
	Verifications []PreventionVerification `json:"verifications"`
}

func (r PreventionGetResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionGet, r.SchemaVersion); err != nil {
		return err
	}
	if err := r.Artifact.Validate(); err != nil {
		return err
	}
	if r.Versions == nil || r.Verifications == nil {
		return errors.New("prevention_get: versions and verifications arrays are required")
	}
	for _, version := range r.Versions {
		if err := version.Validate(); err != nil {
			return err
		}
	}
	for _, verification := range r.Verifications {
		if err := verification.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type PreventionCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

func (c PreventionCount) Validate() error {
	if c.Name == "" {
		return errors.New("prevention.count.name: required")
	}
	if c.Count < 0 {
		return errors.New("prevention.count.count: must not be negative")
	}
	return nil
}

type PreventionActivationStats struct {
	IssueID                 string `json:"issue_id"`
	ArtifactID              string `json:"artifact_id"`
	ArtifactVersionID       string `json:"artifact_version_id"`
	ActivatedAt             string `json:"activated_at"`
	CommitSHA               string `json:"commit_sha"`
	Status                  string `json:"status"`
	FirstSeenToActivationMS int64  `json:"first_seen_to_activation_ms"`
	RecurrenceCount         int64  `json:"recurrence_count"`
	BeforeCount             int64  `json:"before_count"`
	AfterCount              int64  `json:"after_count"`
	BeforeDays              int    `json:"before_days"`
	AfterDays               int    `json:"after_days"`
	ObservationStatus       string `json:"observation_status"`
}

func (s PreventionActivationStats) Validate() error {
	if s.IssueID == "" || s.ArtifactID == "" || s.ArtifactVersionID == "" {
		return errors.New("prevention.activation: issue, artifact and version ids are required")
	}
	if err := validatePreventionTime("prevention.activation.activated_at", s.ActivatedAt); err != nil {
		return err
	}
	if !preventionCommitPattern.MatchString(s.CommitSHA) {
		return errors.New("prevention.activation.commit_sha: want 40 lowercase hex characters")
	}
	if !validPreventionStatuses[s.Status] {
		return fmt.Errorf("prevention.activation.status: unknown %q", s.Status)
	}
	if !validObservationStatuses[s.ObservationStatus] {
		return fmt.Errorf("prevention.activation.observation_status: unknown %q", s.ObservationStatus)
	}
	for _, value := range []int64{s.FirstSeenToActivationMS, s.RecurrenceCount, s.BeforeCount, s.AfterCount, int64(s.BeforeDays), int64(s.AfterDays)} {
		if value < 0 {
			return errors.New("prevention.activation: counts, durations and days must not be negative")
		}
	}
	return nil
}

type PreventionStatsResponse struct {
	SchemaVersion        string                      `json:"schema_version"`
	ByStatus             []PreventionCount           `json:"by_status"`
	ByKind               []PreventionCount           `json:"by_kind"`
	VerificationRuns     int64                       `json:"verification_runs"`
	VerificationPassed   int64                       `json:"verification_passed"`
	VerificationPassRate float64                     `json:"verification_pass_rate"`
	Activations          []PreventionActivationStats `json:"activations"`
	MatureActivations    int64                       `json:"mature_activations"`
	MatureZeroRecurrence int64                       `json:"mature_zero_recurrence"`
	MatureProtectionRate *float64                    `json:"mature_protection_rate"`
	RecurringVersionIDs  []string                    `json:"recurring_version_ids"`
}

func (r PreventionStatsResponse) Validate() error {
	if err := requirePreventionVersion(OperationPreventionStats, r.SchemaVersion); err != nil {
		return err
	}
	if r.ByStatus == nil || r.ByKind == nil || r.Activations == nil || r.RecurringVersionIDs == nil {
		return errors.New("prevention_stats: result arrays are required")
	}
	for _, count := range append(append([]PreventionCount{}, r.ByStatus...), r.ByKind...) {
		if err := count.Validate(); err != nil {
			return err
		}
	}
	for _, count := range []int64{r.VerificationRuns, r.VerificationPassed, r.MatureActivations, r.MatureZeroRecurrence} {
		if count < 0 {
			return errors.New("prevention_stats: counts must not be negative")
		}
	}
	if r.VerificationPassRate < 0 || r.VerificationPassRate > 1 {
		return errors.New("prevention_stats.verification_pass_rate: want 0..1")
	}
	if r.MatureProtectionRate != nil && (*r.MatureProtectionRate < 0 || *r.MatureProtectionRate > 1) {
		return errors.New("prevention_stats.mature_protection_rate: want 0..1 or null")
	}
	for _, activation := range r.Activations {
		if err := activation.Validate(); err != nil {
			return err
		}
	}
	for _, id := range r.RecurringVersionIDs {
		if id == "" {
			return errors.New("prevention_stats.recurring_version_ids: empty id")
		}
	}
	return nil
}
