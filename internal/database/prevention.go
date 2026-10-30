package database

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

// Prevention errors are classified without exposing SQL details to transports.
var (
	ErrPreventionConflict = errors.New("prevention conflict")
	ErrPreventionInvalid  = errors.New("invalid prevention request")
)

func preventionConflict(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrPreventionConflict)
}

func preventionInvalid(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrPreventionInvalid)
}

// PreventionArtifact is the metadata shared by all versions of an artifact.
type PreventionArtifact struct {
	ArtifactID string
	ProjectKey string
	Kind       string
	Title      string
	IssueIDs   []string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PreventionVersion is one immutable artifact body identified by its hash.
type PreventionVersion struct {
	ArtifactVersionID  string
	ArtifactID         string
	Version            int
	ArtifactPath       string
	ContentSHA256      string
	Status             string
	CommitSHA          *string
	ActivatedAt        *time.Time
	CreatedAt          time.Time
	LatestVerification *PreventionVerification
}

// PreventionVerification is one append-only explicit verification run.
type PreventionVerification struct {
	VerificationID    string
	ArtifactVersionID string
	Command           []string
	ExitCode          *int
	Passed            bool
	DurationMS        int64
	Evidence          string
	Environment       map[string]string
	ContentSHA256     string
	CreatedAt         time.Time
}

// PreventionArtifactView groups an artifact with all of its versions.
type PreventionArtifactView struct {
	Artifact PreventionArtifact
	Versions []PreventionVersion
}

// PreventionDetail is the complete read model for one artifact.
type PreventionDetail struct {
	Artifact      PreventionArtifact
	Versions      []PreventionVersion
	Verifications []PreventionVerification
}

// PreventionFilter selects a stable, newest-first artifact page.
type PreventionFilter struct {
	IssueID    string
	ProjectKey string
	Kind       string
	Status     string
	Limit      int
	Cursor     string
}

// PreventionRegistration contains metadata only; artifact bodies and repo_path
// deliberately have no representation in the database layer.
type PreventionRegistration struct {
	IssueID       string
	Kind          string
	Title         string
	ArtifactPath  string
	ContentSHA256 string
	Source        string
	Actor         string
}

// PreventionRegistrationResult distinguishes a new business write from a replay.
type PreventionRegistrationResult struct {
	Artifact PreventionArtifact
	Version  PreventionVersion
	Created  bool
}

// PreventionVerificationInput is one already-executed command result.
type PreventionVerificationInput struct {
	ArtifactVersionID string
	Command           []string
	ExitCode          *int
	Passed            bool
	DurationMS        int64
	Evidence          string
	Environment       map[string]string
	ContentSHA256     string
	Source            string
	Actor             string
}

// PreventionVerificationResult returns the appended run and current version.
type PreventionVerificationResult struct {
	Verification PreventionVerification
	Version      PreventionVersion
}

type preventionQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func preventionIdentity(source, actor string) (string, string) {
	if source == "" {
		source = "cli"
	}
	if actor == "" {
		actor = "cli"
	}
	return redact.Text(source, redact.Public), redact.Text(actor, redact.Public)
}

// RegisterPrevention resolves or creates an artifact, links the resolved issue,
// and appends a draft only when the content hash changes. The advisory lock
// serializes the complete (issue, kind, path) key, including first creation.
func RegisterPrevention(ctx context.Context, conn *pgx.Conn, registration PreventionRegistration) (PreventionRegistrationResult, error) {
	if registration.IssueID == "" || registration.Kind == "" || registration.Title == "" || registration.ArtifactPath == "" || registration.ContentSHA256 == "" {
		return PreventionRegistrationResult{}, preventionInvalid("registration metadata is incomplete")
	}
	source, actor := preventionIdentity(registration.Source, registration.Actor)
	title := redact.Text(registration.Title, redact.Public)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := registration.IssueID + "|" + registration.Kind + "|" + registration.ArtifactPath
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: lock: %w", err)
	}
	var issueProject, issueStatus string
	if err := tx.QueryRow(ctx, `select project_key, status from issues where id = $1`, registration.IssueID).Scan(&issueProject, &issueStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionRegistrationResult{}, fmt.Errorf("register prevention: issue %s: %w", registration.IssueID, pgx.ErrNoRows)
		}
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: read issue: %w", err)
	}
	if issueStatus != "resolved" {
		return PreventionRegistrationResult{}, preventionConflict("issue %s is %s, not resolved", registration.IssueID, issueStatus)
	}

	var artifactID, artifactProject string
	err = tx.QueryRow(ctx, `
		select a.id, a.project_key from prevention_artifacts a
		join prevention_artifact_versions v on v.artifact_id = a.id
		where a.kind = $1 and v.artifact_path = $2
		order by a.created_at, a.id limit 1 for update of a
	`, registration.Kind, registration.ArtifactPath).Scan(&artifactID, &artifactProject)
	artifactCreated := false
	if errors.Is(err, pgx.ErrNoRows) {
		artifactCreated = true
		if err := tx.QueryRow(ctx, `
			insert into prevention_artifacts (project_key, kind, title, source, actor)
			values ($1, $2, $3, $4, $5) returning id
		`, issueProject, registration.Kind, title, source, actor).Scan(&artifactID); err != nil {
			return PreventionRegistrationResult{}, fmt.Errorf("register prevention: create artifact: %w", err)
		}
		if err := insertAudit(ctx, tx, "prevention.artifact.create", "prevention_artifact", artifactID, actor, source, map[string]any{
			"project_key": issueProject, "kind": registration.Kind, "title": title,
		}); err != nil {
			return PreventionRegistrationResult{}, err
		}
	} else if err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: find artifact: %w", err)
	} else if artifactProject != issueProject {
		return PreventionRegistrationResult{}, preventionConflict("artifact %s belongs to project %s, not %s", artifactID, artifactProject, issueProject)
	}

	var linked bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from issue_preventions where issue_id = $1 and artifact_id = $2)`, registration.IssueID, artifactID).Scan(&linked); err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: check issue link: %w", err)
	}
	linkCreated := false
	if !linked {
		linkCreated = true
		if _, err := tx.Exec(ctx, `insert into issue_preventions (issue_id, artifact_id, source, actor) values ($1, $2, $3, $4)`, registration.IssueID, artifactID, source, actor); err != nil {
			return PreventionRegistrationResult{}, fmt.Errorf("register prevention: link issue: %w", err)
		}
		if err := insertAudit(ctx, tx, "prevention.issue.link", "prevention_artifact", artifactID, actor, source, map[string]any{"issue_id": registration.IssueID}); err != nil {
			return PreventionRegistrationResult{}, err
		}
	}

	var versionID string
	err = tx.QueryRow(ctx, `
		select id from prevention_artifact_versions
		where artifact_id = $1 and content_sha256 = $2
		order by version desc limit 1
	`, artifactID, registration.ContentSHA256).Scan(&versionID)
	versionCreated := false
	if errors.Is(err, pgx.ErrNoRows) {
		versionCreated = true
		if err := tx.QueryRow(ctx, `
			insert into prevention_artifact_versions
				(artifact_id, version, artifact_path, content_sha256, source, actor)
			select $1, coalesce(max(version), 0) + 1, $2, $3, $4, $5
			from prevention_artifact_versions where artifact_id = $1
			returning id
		`, artifactID, registration.ArtifactPath, registration.ContentSHA256, source, actor).Scan(&versionID); err != nil {
			return PreventionRegistrationResult{}, fmt.Errorf("register prevention: append version: %w", err)
		}
		if err := insertAudit(ctx, tx, "prevention.version.create", "prevention_version", versionID, actor, source, map[string]any{
			"artifact_id": artifactID, "artifact_path": registration.ArtifactPath, "content_sha256": registration.ContentSHA256,
		}); err != nil {
			return PreventionRegistrationResult{}, err
		}
	} else if err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: find version: %w", err)
	}
	if artifactCreated || linkCreated || versionCreated {
		if _, err := tx.Exec(ctx, `update prevention_artifacts set updated_at = now() where id = $1`, artifactID); err != nil {
			return PreventionRegistrationResult{}, fmt.Errorf("register prevention: touch artifact: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PreventionRegistrationResult{}, fmt.Errorf("register prevention: commit: %w", err)
	}
	artifact, versions, err := loadPreventionArtifact(ctx, conn, artifactID)
	if err != nil {
		return PreventionRegistrationResult{}, err
	}
	for _, version := range versions {
		if version.ArtifactVersionID == versionID {
			return PreventionRegistrationResult{Artifact: artifact, Version: version, Created: artifactCreated || linkCreated || versionCreated}, nil
		}
	}
	return PreventionRegistrationResult{}, fmt.Errorf("register prevention: saved version %s: %w", versionID, pgx.ErrNoRows)
}

// GetPreventionVersion reads one version and its latest verification.
func GetPreventionVersion(ctx context.Context, conn *pgx.Conn, versionID string) (PreventionVersion, error) {
	var artifactID string
	if err := conn.QueryRow(ctx, `select artifact_id from prevention_artifact_versions where id = $1`, versionID).Scan(&artifactID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionVersion{}, fmt.Errorf("prevention version %s: %w", versionID, pgx.ErrNoRows)
		}
		return PreventionVersion{}, fmt.Errorf("get prevention version: %w", err)
	}
	_, versions, err := loadPreventionArtifact(ctx, conn, artifactID)
	if err != nil {
		return PreventionVersion{}, err
	}
	for _, version := range versions {
		if version.ArtifactVersionID == versionID {
			return version, nil
		}
	}
	return PreventionVersion{}, fmt.Errorf("prevention version %s: %w", versionID, pgx.ErrNoRows)
}

// RecordPreventionVerification appends one run. A failure never rewrites the
// historical state, but it becomes the latest run and therefore blocks activate.
func RecordPreventionVerification(ctx context.Context, conn *pgx.Conn, input PreventionVerificationInput) (PreventionVerificationResult, error) {
	if input.ArtifactVersionID == "" || len(input.Command) == 0 || input.ContentSHA256 == "" || input.DurationMS < 0 {
		return PreventionVerificationResult{}, preventionInvalid("verification metadata is incomplete")
	}
	source, actor := preventionIdentity(input.Source, input.Actor)
	command := make([]string, len(input.Command))
	for index, argument := range input.Command {
		command[index] = redact.Text(argument, redact.Public)
	}
	encodedCommand, err := json.Marshal(command)
	if err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: encode command: %w", err)
	}
	environment := make(map[string]string)
	for _, key := range []string{"os", "shell", "go_version", "git_version"} {
		if value := input.Environment[key]; value != "" {
			environment[key] = redact.Text(value, redact.Public)
		}
	}
	encodedEnvironment, err := json.Marshal(environment)
	if err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: encode environment: %w", err)
	}
	evidence := redact.Sample(input.Evidence, redact.Public)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var artifactID, registeredHash, status string
	if err := tx.QueryRow(ctx, `
		select a.id, v.content_sha256, v.status
		from prevention_artifact_versions v join prevention_artifacts a on a.id = v.artifact_id
		where v.id = $1 for update of a, v
	`, input.ArtifactVersionID).Scan(&artifactID, &registeredHash, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionVerificationResult{}, fmt.Errorf("prevention version %s: %w", input.ArtifactVersionID, pgx.ErrNoRows)
		}
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: lock version: %w", err)
	}
	if status != "draft" && status != "verified" {
		return PreventionVerificationResult{}, preventionConflict("version %s is %s and cannot be verified", input.ArtifactVersionID, status)
	}
	if registeredHash != input.ContentSHA256 {
		return PreventionVerificationResult{}, preventionConflict("verification hash does not match registered artifact hash")
	}
	verification := PreventionVerification{
		ArtifactVersionID: input.ArtifactVersionID, Command: command, ExitCode: input.ExitCode,
		Passed: input.Passed, DurationMS: input.DurationMS, Evidence: evidence,
		Environment: environment, ContentSHA256: input.ContentSHA256,
	}
	if err := tx.QueryRow(ctx, `
		insert into prevention_verifications
			(artifact_version_id, command, exit_code, passed, duration_ms, evidence, environment, content_sha256, source, actor)
		values ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9, $10) returning id, created_at
	`, input.ArtifactVersionID, encodedCommand, input.ExitCode, input.Passed, input.DurationMS,
		evidence, encodedEnvironment, input.ContentSHA256, source, actor).Scan(&verification.VerificationID, &verification.CreatedAt); err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: insert: %w", err)
	}
	if input.Passed {
		if _, err := tx.Exec(ctx, `update prevention_artifact_versions set status = 'verified' where id = $1`, input.ArtifactVersionID); err != nil {
			return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: verify version: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `update prevention_artifacts set updated_at = now() where id = $1`, artifactID); err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: touch artifact: %w", err)
	}
	if err := insertAudit(ctx, tx, "prevention.verify", "prevention_version", input.ArtifactVersionID, actor, source, map[string]any{
		"verification_id": verification.VerificationID, "passed": input.Passed, "exit_code": input.ExitCode,
	}); err != nil {
		return PreventionVerificationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PreventionVerificationResult{}, fmt.Errorf("record prevention verification: commit: %w", err)
	}
	version, err := GetPreventionVersion(ctx, conn, input.ArtifactVersionID)
	if err != nil {
		return PreventionVerificationResult{}, err
	}
	return PreventionVerificationResult{Verification: verification, Version: version}, nil
}

// ActivatePrevention binds a verified version to one immutable Git commit. An
// artifact lock serializes replacement so its partial unique index cannot race.
func ActivatePrevention(ctx context.Context, conn *pgx.Conn, versionID, commitSHA string) (PreventionVersion, bool, error) {
	if versionID == "" || len(commitSHA) != 40 {
		return PreventionVersion{}, false, preventionInvalid("version id and a 40-character commit are required")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var artifactID, status, registeredHash string
	var activeCommit *string
	if err := tx.QueryRow(ctx, `
		select a.id, v.status, v.content_sha256, v.commit_sha
		from prevention_artifact_versions v join prevention_artifacts a on a.id = v.artifact_id
		where v.id = $1 for update of a, v
	`, versionID).Scan(&artifactID, &status, &registeredHash, &activeCommit); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionVersion{}, false, fmt.Errorf("prevention version %s: %w", versionID, pgx.ErrNoRows)
		}
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: lock version: %w", err)
	}
	if status == "active" {
		if activeCommit != nil && *activeCommit == commitSHA {
			if err := tx.Commit(ctx); err != nil {
				return PreventionVersion{}, false, fmt.Errorf("activate prevention: replay commit: %w", err)
			}
			version, err := GetPreventionVersion(ctx, conn, versionID)
			return version, false, err
		}
		return PreventionVersion{}, false, preventionConflict("active version %s is bound to another commit", versionID)
	}
	if status != "verified" {
		return PreventionVersion{}, false, preventionConflict("version %s is %s, not verified", versionID, status)
	}
	var latestPassed bool
	var latestHash string
	if err := tx.QueryRow(ctx, `
		select passed, content_sha256 from prevention_verifications
		where artifact_version_id = $1 order by created_at desc, id desc limit 1
	`, versionID).Scan(&latestPassed, &latestHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionVersion{}, false, preventionConflict("version %s has no verification", versionID)
		}
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: latest verification: %w", err)
	}
	if !latestPassed || latestHash != registeredHash {
		return PreventionVersion{}, false, preventionConflict("latest verification is not a passing match for version %s", versionID)
	}
	if _, err := tx.Exec(ctx, `update prevention_artifact_versions set status = 'deprecated' where artifact_id = $1 and status = 'active'`, artifactID); err != nil {
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: deprecate old version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update prevention_artifact_versions
		set status = 'active', commit_sha = $2, activated_at = now() where id = $1
	`, versionID, commitSHA); err != nil {
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: activate version: %w", err)
	}
	if _, err := tx.Exec(ctx, `update prevention_artifacts set updated_at = now() where id = $1`, artifactID); err != nil {
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: touch artifact: %w", err)
	}
	if err := insertAudit(ctx, tx, "prevention.activate", "prevention_version", versionID, "cli", "cli", map[string]any{
		"artifact_id": artifactID, "commit_sha": commitSHA,
	}); err != nil {
		return PreventionVersion{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PreventionVersion{}, false, fmt.Errorf("activate prevention: commit: %w", err)
	}
	version, err := GetPreventionVersion(ctx, conn, versionID)
	return version, err == nil, err
}

type preventionCursor struct {
	UpdatedAt  time.Time `json:"updated_at"`
	ArtifactID string    `json:"artifact_id"`
}

func encodePreventionCursor(cursor preventionCursor) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePreventionCursor(value string) (preventionCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return preventionCursor{}, preventionInvalid("malformed prevention cursor")
	}
	var cursor preventionCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.UpdatedAt.IsZero() || cursor.ArtifactID == "" {
		return preventionCursor{}, preventionInvalid("malformed prevention cursor")
	}
	return cursor, nil
}

// ListPreventions returns newest artifacts first with (updated_at, id) as a
// deterministic tie-breaker. Status filters artifacts having such a version.
func ListPreventions(ctx context.Context, conn *pgx.Conn, filter PreventionFilter) ([]PreventionArtifactView, string, error) {
	limit := filter.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return nil, "", preventionInvalid("prevention list limit must be between 1 and 100")
	}
	conditions := []string{}
	arguments := []any{}
	addCondition := func(condition string, value any) {
		arguments = append(arguments, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(arguments)))
	}
	if filter.IssueID != "" {
		addCondition(`exists(select 1 from issue_preventions ip where ip.artifact_id = a.id and ip.issue_id = $%d)`, filter.IssueID)
	}
	if filter.ProjectKey != "" {
		addCondition(`a.project_key = $%d`, filter.ProjectKey)
	}
	if filter.Kind != "" {
		addCondition(`a.kind = $%d`, filter.Kind)
	}
	if filter.Status != "" {
		addCondition(`exists(select 1 from prevention_artifact_versions fv where fv.artifact_id = a.id and fv.status = $%d)`, filter.Status)
	}
	if filter.Cursor != "" {
		cursor, err := decodePreventionCursor(filter.Cursor)
		if err != nil {
			return nil, "", err
		}
		arguments = append(arguments, cursor.UpdatedAt, cursor.ArtifactID)
		conditions = append(conditions, fmt.Sprintf(`(a.updated_at, a.id) < ($%d, $%d::uuid)`, len(arguments)-1, len(arguments)))
	}
	where := ""
	if len(conditions) > 0 {
		where = " where " + strings.Join(conditions, " and ")
	}
	arguments = append(arguments, limit+1)
	rows, err := conn.Query(ctx, `select a.id from prevention_artifacts a`+where+
		fmt.Sprintf(` order by a.updated_at desc, a.id desc limit $%d`, len(arguments)), arguments...)
	if err != nil {
		return nil, "", fmt.Errorf("list preventions: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("list preventions: scan: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list preventions: rows: %w", err)
	}
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		last := ids[len(ids)-1]
		var updatedAt time.Time
		if err := conn.QueryRow(ctx, `select updated_at from prevention_artifacts where id = $1`, last).Scan(&updatedAt); err != nil {
			return nil, "", fmt.Errorf("list preventions: cursor: %w", err)
		}
		next, err = encodePreventionCursor(preventionCursor{UpdatedAt: updatedAt, ArtifactID: last})
		if err != nil {
			return nil, "", fmt.Errorf("list preventions: encode cursor: %w", err)
		}
	}
	items := make([]PreventionArtifactView, 0, len(ids))
	for _, id := range ids {
		artifact, versions, err := loadPreventionArtifact(ctx, conn, id)
		if err != nil {
			return nil, "", err
		}
		items = append(items, PreventionArtifactView{Artifact: artifact, Versions: versions})
	}
	return items, next, nil
}

// GetPrevention returns all versions and append-only verification history.
func GetPrevention(ctx context.Context, conn *pgx.Conn, artifactID string) (PreventionDetail, error) {
	artifact, versions, err := loadPreventionArtifact(ctx, conn, artifactID)
	if err != nil {
		return PreventionDetail{}, err
	}
	rows, err := conn.Query(ctx, `
		select r.id, r.artifact_version_id, r.command, r.exit_code, r.passed, r.duration_ms,
			r.evidence, r.environment, r.content_sha256, r.created_at
		from prevention_verifications r join prevention_artifact_versions v on v.id = r.artifact_version_id
		where v.artifact_id = $1 order by r.created_at, r.id
	`, artifactID)
	if err != nil {
		return PreventionDetail{}, fmt.Errorf("get prevention: verifications: %w", err)
	}
	defer rows.Close()
	verifications := []PreventionVerification{}
	for rows.Next() {
		verification, err := scanPreventionVerification(rows)
		if err != nil {
			return PreventionDetail{}, err
		}
		verifications = append(verifications, verification)
	}
	if err := rows.Err(); err != nil {
		return PreventionDetail{}, fmt.Errorf("get prevention: verification rows: %w", err)
	}
	return PreventionDetail{Artifact: artifact, Versions: versions, Verifications: verifications}, nil
}

func loadPreventionArtifact(ctx context.Context, conn preventionQuerier, artifactID string) (PreventionArtifact, []PreventionVersion, error) {
	artifact := PreventionArtifact{ArtifactID: artifactID, IssueIDs: []string{}}
	if err := conn.QueryRow(ctx, `select project_key, kind, title, created_at, updated_at from prevention_artifacts where id = $1`, artifactID).Scan(
		&artifact.ProjectKey, &artifact.Kind, &artifact.Title, &artifact.CreatedAt, &artifact.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreventionArtifact{}, nil, fmt.Errorf("prevention artifact %s: %w", artifactID, pgx.ErrNoRows)
		}
		return PreventionArtifact{}, nil, fmt.Errorf("load prevention artifact: %w", err)
	}
	issueRows, err := conn.Query(ctx, `select issue_id from issue_preventions where artifact_id = $1 order by issue_id`, artifactID)
	if err != nil {
		return PreventionArtifact{}, nil, fmt.Errorf("load prevention issue links: %w", err)
	}
	for issueRows.Next() {
		var issueID string
		if err := issueRows.Scan(&issueID); err != nil {
			issueRows.Close()
			return PreventionArtifact{}, nil, fmt.Errorf("load prevention issue link: %w", err)
		}
		artifact.IssueIDs = append(artifact.IssueIDs, issueID)
	}
	issueRows.Close()
	if err := issueRows.Err(); err != nil {
		return PreventionArtifact{}, nil, fmt.Errorf("load prevention issue links: %w", err)
	}
	rows, err := conn.Query(ctx, `
		select id, version, artifact_path, content_sha256, status, commit_sha, activated_at, created_at
		from prevention_artifact_versions where artifact_id = $1 order by version
	`, artifactID)
	if err != nil {
		return PreventionArtifact{}, nil, fmt.Errorf("load prevention versions: %w", err)
	}
	versions := []PreventionVersion{}
	for rows.Next() {
		version := PreventionVersion{ArtifactID: artifactID}
		if err := rows.Scan(&version.ArtifactVersionID, &version.Version, &version.ArtifactPath,
			&version.ContentSHA256, &version.Status, &version.CommitSHA, &version.ActivatedAt, &version.CreatedAt); err != nil {
			rows.Close()
			return PreventionArtifact{}, nil, fmt.Errorf("load prevention version: %w", err)
		}
		versions = append(versions, version)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PreventionArtifact{}, nil, fmt.Errorf("load prevention versions: %w", err)
	}
	for index := range versions {
		latest, err := latestPreventionVerification(ctx, conn, versions[index].ArtifactVersionID)
		if err != nil {
			return PreventionArtifact{}, nil, err
		}
		versions[index].LatestVerification = latest
	}
	return artifact, versions, nil
}

func latestPreventionVerification(ctx context.Context, conn preventionQuerier, versionID string) (*PreventionVerification, error) {
	rows, err := conn.Query(ctx, `
		select id, artifact_version_id, command, exit_code, passed, duration_ms, evidence, environment, content_sha256, created_at
		from prevention_verifications where artifact_version_id = $1 order by created_at desc, id desc limit 1
	`, versionID)
	if err != nil {
		return nil, fmt.Errorf("load latest prevention verification: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	verification, err := scanPreventionVerification(rows)
	if err != nil {
		return nil, err
	}
	return &verification, nil
}

func scanPreventionVerification(rows pgx.Rows) (PreventionVerification, error) {
	verification := PreventionVerification{Environment: map[string]string{}}
	var command, environment []byte
	if err := rows.Scan(&verification.VerificationID, &verification.ArtifactVersionID, &command,
		&verification.ExitCode, &verification.Passed, &verification.DurationMS, &verification.Evidence,
		&environment, &verification.ContentSHA256, &verification.CreatedAt); err != nil {
		return PreventionVerification{}, fmt.Errorf("scan prevention verification: %w", err)
	}
	if err := json.Unmarshal(command, &verification.Command); err != nil {
		return PreventionVerification{}, fmt.Errorf("scan prevention verification command: %w", err)
	}
	if err := json.Unmarshal(environment, &verification.Environment); err != nil {
		return PreventionVerification{}, fmt.Errorf("scan prevention verification environment: %w", err)
	}
	if verification.Command == nil {
		verification.Command = []string{}
	}
	if verification.Environment == nil {
		verification.Environment = map[string]string{}
	}
	return verification, nil
}
