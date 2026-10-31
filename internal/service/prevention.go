package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

const (
	preventionArtifactMaxBytes    = 1 << 20
	preventionVerificationTimeout = 120 * time.Second
	preventionGitTimeout          = 30 * time.Second
)

var preventionCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func preventionExecutionConflict(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), database.ErrPreventionConflict)
}

func validatePreventionRelativePath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, `\`) || filepath.IsAbs(filepath.FromSlash(path)) {
		return preventionExecutionConflict("artifact path %q is not a repository-relative slash path", path)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.EqualFold(segment, ".git") {
			return preventionExecutionConflict("artifact path %q contains a forbidden segment", path)
		}
	}
	return nil
}

// resolvePreventionFile confines a regular file to the canonical repository.
// Every path component is checked with Lstat so a symlink cannot redirect the
// read outside the repository or into Git's metadata.
func resolvePreventionFile(repo, path string) (string, error) {
	if err := validatePreventionRelativePath(path); err != nil {
		return "", err
	}
	absoluteRepo, err := filepath.Abs(repo)
	if err != nil {
		return "", preventionExecutionConflict("resolve repository: %v", err)
	}
	resolvedRepo, err := filepath.EvalSymlinks(absoluteRepo)
	if err != nil {
		return "", preventionExecutionConflict("resolve repository: %v", err)
	}
	info, err := os.Stat(resolvedRepo)
	if err != nil || !info.IsDir() {
		return "", preventionExecutionConflict("repository %q is not a readable directory", repo)
	}
	current := resolvedRepo
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		entry, err := os.Lstat(current)
		if err != nil {
			return "", preventionExecutionConflict("read artifact %q: %v", path, err)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return "", preventionExecutionConflict("artifact %q contains a symbolic link", path)
		}
		if index < len(segments)-1 && !entry.IsDir() {
			return "", preventionExecutionConflict("artifact %q has a non-directory parent", path)
		}
		if index == len(segments)-1 {
			if !entry.Mode().IsRegular() {
				return "", preventionExecutionConflict("artifact %q is not a regular file", path)
			}
			if entry.Size() > preventionArtifactMaxBytes {
				return "", preventionExecutionConflict("artifact %q exceeds 1 MiB", path)
			}
		}
	}
	resolvedFile, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", preventionExecutionConflict("resolve artifact %q: %v", path, err)
	}
	relative, err := filepath.Rel(resolvedRepo, resolvedFile)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", preventionExecutionConflict("artifact %q escapes the repository", path)
	}
	return resolvedFile, nil
}

// HashPreventionArtifact is the single file-safety and hashing entry point used
// by both the CLI registration path and the service's verification path.
func HashPreventionArtifact(repo, path string) (string, error) {
	resolved, err := resolvePreventionFile(repo, path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", preventionExecutionConflict("open artifact %q: %v", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	read, err := io.Copy(hash, io.LimitReader(file, preventionArtifactMaxBytes+1))
	if err != nil {
		return "", preventionExecutionConflict("hash artifact %q: %v", path, err)
	}
	if read > preventionArtifactMaxBytes {
		return "", preventionExecutionConflict("artifact %q exceeds 1 MiB", path)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// executePreventionVerification turns every command outcome into persistable
// evidence. Only a pre/post filesystem infrastructure error is returned as a
// Go error; non-zero exits, start failures and deadlines are normal failed runs.
func executePreventionVerification(ctx context.Context, repo string, version database.PreventionVersion, command []string) (database.PreventionVerificationInput, error) {
	before, err := HashPreventionArtifact(repo, version.ArtifactPath)
	if err != nil {
		return database.PreventionVerificationInput{}, err
	}
	if before != version.ContentSHA256 {
		return database.PreventionVerificationInput{}, preventionExecutionConflict("artifact content changed before verification")
	}
	runContext, cancel := context.WithTimeout(ctx, preventionVerificationTimeout)
	defer cancel()
	started := time.Now()
	result, runErr := capture.Run(runContext, capture.RunOptions{
		Command: command, Dir: repo, Stdin: strings.NewReader(""),
		Stdout: io.Discard, Stderr: io.Discard, CaptureLimit: capture.DefaultCaptureLimit, Recorder: nil,
	})
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	passed := runErr == nil && result.ExitCode == 0 && !errors.Is(runContext.Err(), context.DeadlineExceeded)
	notes := []string{}
	if runErr != nil {
		notes = append(notes, "command start failed: "+runErr.Error())
	}
	if errors.Is(runContext.Err(), context.DeadlineExceeded) {
		passed = false
		notes = append(notes, "verification timed out")
	}
	if result.StdoutDropped > 0 {
		notes = append(notes, fmt.Sprintf("stdout truncated: %d bytes omitted", result.StdoutDropped))
	}
	if result.StderrDropped > 0 {
		notes = append(notes, fmt.Sprintf("stderr truncated: %d bytes omitted", result.StderrDropped))
	}
	after, hashErr := HashPreventionArtifact(repo, version.ArtifactPath)
	if hashErr != nil {
		passed = false
		notes = append(notes, "artifact could not be read after verification: "+hashErr.Error())
	} else if after != before {
		passed = false
		notes = append(notes, "artifact content changed during verification")
	}
	evidenceParts := append([]string{}, notes...)
	if result.Stdout != "" {
		evidenceParts = append(evidenceParts, "stdout:\n"+redact.Sample(result.Stdout, redact.Public))
	}
	if result.Stderr != "" {
		evidenceParts = append(evidenceParts, "stderr:\n"+redact.Sample(result.Stderr, redact.Public))
	}
	evidence := redact.Sample(strings.Join(evidenceParts, "\n"), redact.Public)
	var exitCode *int
	if runErr == nil {
		code := result.ExitCode
		exitCode = &code
	}
	summary := capture.SummarizeEnvironment()
	environment := map[string]string{"os": summary.OS}
	if summary.Shell != "" {
		environment["shell"] = summary.Shell
	}
	// The frozen v1 response contract has os/shell/go_version/git_version keys;
	// architecture remains available from capture but is not persisted here.
	if goVersion := summary.RuntimeVersions["go"]; goVersion != "" {
		environment["go_version"] = goVersion
	}
	return database.PreventionVerificationInput{
		ArtifactVersionID: version.ArtifactVersionID, Command: append([]string(nil), command...),
		ExitCode: exitCode, Passed: passed, DurationMS: duration, Evidence: evidence,
		Environment: environment, ContentSHA256: before, Source: "cli", Actor: "cli",
	}, nil
}

type preventionGitResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func runReadOnlyGit(ctx context.Context, repo string, arguments ...string) (preventionGitResult, error) {
	gitContext, cancel := context.WithTimeout(ctx, preventionGitTimeout)
	defer cancel()
	result, err := capture.Run(gitContext, capture.RunOptions{
		Command: append([]string{"git"}, arguments...), Dir: repo, Stdin: strings.NewReader(""),
		Stdout: io.Discard, Stderr: io.Discard, CaptureLimit: 4096, Recorder: nil,
	})
	if err != nil {
		return preventionGitResult{}, preventionExecutionConflict("git %s could not start: %v", arguments[0], err)
	}
	return preventionGitResult{stdout: result.Stdout, stderr: result.Stderr, exitCode: result.ExitCode}, nil
}

// preventionGitCommit proves that the artifact is clean and tracked at HEAD.
// Every subprocess is read-only and its retained output is bounded.
func preventionGitCommit(ctx context.Context, repo, path string) (string, error) {
	if err := validatePreventionRelativePath(path); err != nil {
		return "", err
	}
	rootResult, err := runReadOnlyGit(ctx, repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if rootResult.exitCode != 0 {
		return "", preventionExecutionConflict("not a readable Git repository: %s", redact.Sample(rootResult.stderr, redact.Public))
	}
	root := strings.TrimSpace(rootResult.stdout)
	if root == "" {
		return "", preventionExecutionConflict("Git returned an empty repository root")
	}
	if _, err := resolvePreventionFile(root, path); err != nil {
		return "", err
	}
	headResult, err := runReadOnlyGit(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(headResult.stdout)
	if headResult.exitCode != 0 || !preventionCommitSHA.MatchString(head) {
		return "", preventionExecutionConflict("Git HEAD is missing or invalid")
	}
	tracked, err := runReadOnlyGit(ctx, root, "cat-file", "-e", "HEAD:"+path)
	if err != nil {
		return "", err
	}
	if tracked.exitCode != 0 {
		return "", preventionExecutionConflict("artifact %q is not tracked at HEAD", path)
	}
	status, err := runReadOnlyGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--", ":(literal)"+path)
	if err != nil {
		return "", err
	}
	if status.exitCode != 0 {
		return "", preventionExecutionConflict("Git status failed: %s", redact.Sample(status.stderr, redact.Public))
	}
	if strings.TrimSpace(status.stdout) != "" {
		return "", preventionExecutionConflict("artifact %q has uncommitted changes", path)
	}
	return head, nil
}

// PreventionAdd registers metadata only. Hashing the local file belongs to the
// CLI add path, which calls HashPreventionArtifact before building the request.
func (b *SQLBackend) PreventionAdd(ctx context.Context, request contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.PreventionAddResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var stored database.PreventionRegistrationResult
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.RegisterPrevention(ctx, conn, database.PreventionRegistration{
			IssueID: request.IssueID, Kind: request.Kind, Title: request.Title,
			ArtifactPath: request.ArtifactPath, ContentSHA256: request.ContentSHA256,
			Source: request.Source, Actor: request.Actor,
		})
		if err == nil {
			stored = result
		}
		return err
	})
	if err != nil {
		return contract.PreventionAddResponse{}, translateBackendError("prevention_add", err)
	}
	response := contract.PreventionAddResponse{
		SchemaVersion: contract.PreventionSchemaVersion,
		Artifact:      mapPreventionArtifact(stored.Artifact), Version: mapPreventionVersion(stored.Version), Created: stored.Created,
	}
	if err := response.Validate(); err != nil {
		return contract.PreventionAddResponse{}, APIErrorf(CodeInternal, "prevention_add response: %v", err)
	}
	return response, nil
}

func (b *SQLBackend) preventionVersion(ctx context.Context, versionID string) (database.PreventionVersion, error) {
	var version database.PreventionVersion
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		stored, err := database.GetPreventionVersion(ctx, conn, versionID)
		if err == nil {
			version = stored
		}
		return err
	})
	return version, err
}

// PreventionVerify executes outside a database transaction/connection, then
// appends the normal pass/fail result through the state machine.
func (b *SQLBackend) PreventionVerify(ctx context.Context, request contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.PreventionVerifyResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	version, err := b.preventionVersion(ctx, request.ArtifactVersionID)
	if err != nil {
		return contract.PreventionVerifyResponse{}, translateBackendError("prevention_verify", err)
	}
	input, err := executePreventionVerification(ctx, request.RepoPath, version, request.Command)
	if err != nil {
		return contract.PreventionVerifyResponse{}, translateBackendError("prevention_verify", err)
	}
	var stored database.PreventionVerificationResult
	err = b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.RecordPreventionVerification(ctx, conn, input)
		if err == nil {
			stored = result
		}
		return err
	})
	if err != nil {
		return contract.PreventionVerifyResponse{}, translateBackendError("prevention_verify", err)
	}
	response := contract.PreventionVerifyResponse{
		SchemaVersion: contract.PreventionSchemaVersion,
		Verification:  mapPreventionVerification(stored.Verification), Version: mapPreventionVersion(stored.Version),
	}
	if err := response.Validate(); err != nil {
		return contract.PreventionVerifyResponse{}, APIErrorf(CodeInternal, "prevention_verify response: %v", err)
	}
	return response, nil
}

// PreventionActivate recomputes the file hash and obtains clean Git evidence
// before entering the transactional activation state machine.
func (b *SQLBackend) PreventionActivate(ctx context.Context, request contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.PreventionActivateResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	version, err := b.preventionVersion(ctx, request.ArtifactVersionID)
	if err != nil {
		return contract.PreventionActivateResponse{}, translateBackendError("prevention_activate", err)
	}
	hash, err := HashPreventionArtifact(request.RepoPath, version.ArtifactPath)
	if err != nil {
		return contract.PreventionActivateResponse{}, translateBackendError("prevention_activate", err)
	}
	if hash != version.ContentSHA256 {
		return contract.PreventionActivateResponse{}, translateBackendError("prevention_activate", preventionExecutionConflict("artifact content does not match version %s", version.ArtifactVersionID))
	}
	commit, err := preventionGitCommit(ctx, request.RepoPath, version.ArtifactPath)
	if err != nil {
		return contract.PreventionActivateResponse{}, translateBackendError("prevention_activate", err)
	}
	var stored database.PreventionVersion
	activated := false
	err = b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, changed, err := database.ActivatePrevention(ctx, conn, version.ArtifactVersionID, commit)
		if err == nil {
			stored, activated = result, changed
		}
		return err
	})
	if err != nil {
		return contract.PreventionActivateResponse{}, translateBackendError("prevention_activate", err)
	}
	response := contract.PreventionActivateResponse{
		SchemaVersion: contract.PreventionSchemaVersion, Version: mapPreventionVersion(stored), Activated: activated,
	}
	if err := response.Validate(); err != nil {
		return contract.PreventionActivateResponse{}, APIErrorf(CodeInternal, "prevention_activate response: %v", err)
	}
	return response, nil
}

// PreventionList maps a stable storage page into required non-nil arrays.
func (b *SQLBackend) PreventionList(ctx context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.PreventionListResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	limit := DefaultListLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	var items []database.PreventionArtifactView
	var next string
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		stored, cursor, err := database.ListPreventions(ctx, conn, database.PreventionFilter{
			IssueID: request.IssueID, ProjectKey: request.ProjectKey, Kind: request.Kind,
			Status: request.Status, Limit: limit, Cursor: request.Cursor,
		})
		if err == nil {
			items, next = stored, cursor
		}
		return err
	})
	if err != nil {
		return contract.PreventionListResponse{}, translateBackendError("prevention_list", err)
	}
	response := contract.PreventionListResponse{SchemaVersion: contract.PreventionSchemaVersion, Items: make([]contract.PreventionArtifactView, 0, len(items)), NextCursor: next}
	for _, item := range items {
		versions := make([]contract.PreventionVersion, 0, len(item.Versions))
		for _, version := range item.Versions {
			versions = append(versions, mapPreventionVersion(version))
		}
		response.Items = append(response.Items, contract.PreventionArtifactView{Artifact: mapPreventionArtifact(item.Artifact), Versions: versions})
	}
	if err := response.Validate(); err != nil {
		return contract.PreventionListResponse{}, APIErrorf(CodeInternal, "prevention_list response: %v", err)
	}
	return response, nil
}

// PreventionGet returns metadata and append-only verification history only.
func (b *SQLBackend) PreventionGet(ctx context.Context, request contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.PreventionGetResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var stored database.PreventionDetail
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		detail, err := database.GetPrevention(ctx, conn, request.ArtifactID)
		if err == nil {
			stored = detail
		}
		return err
	})
	if err != nil {
		return contract.PreventionGetResponse{}, translateBackendError("prevention_get", err)
	}
	response := contract.PreventionGetResponse{
		SchemaVersion: contract.PreventionSchemaVersion, Artifact: mapPreventionArtifact(stored.Artifact),
		Versions:      make([]contract.PreventionVersion, 0, len(stored.Versions)),
		Verifications: make([]contract.PreventionVerification, 0, len(stored.Verifications)),
	}
	for _, version := range stored.Versions {
		response.Versions = append(response.Versions, mapPreventionVersion(version))
	}
	for _, verification := range stored.Verifications {
		response.Verifications = append(response.Verifications, mapPreventionVerification(verification))
	}
	if err := response.Validate(); err != nil {
		return contract.PreventionGetResponse{}, APIErrorf(CodeInternal, "prevention_get response: %v", err)
	}
	return response, nil
}

func mapPreventionArtifact(artifact database.PreventionArtifact) contract.PreventionArtifact {
	issueIDs := append([]string{}, artifact.IssueIDs...)
	return contract.PreventionArtifact{
		ArtifactID: artifact.ArtifactID, ProjectKey: artifact.ProjectKey, Kind: artifact.Kind,
		Title: artifact.Title, IssueIDs: issueIDs,
		CreatedAt: artifact.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: artifact.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func mapPreventionVersion(version database.PreventionVersion) contract.PreventionVersion {
	mapped := contract.PreventionVersion{
		ArtifactVersionID: version.ArtifactVersionID, ArtifactID: version.ArtifactID, Version: version.Version,
		ArtifactPath: version.ArtifactPath, ContentSHA256: version.ContentSHA256, Status: version.Status,
		CreatedAt: version.CreatedAt.UTC().Format(time.RFC3339),
	}
	if version.CommitSHA != nil {
		commit := *version.CommitSHA
		mapped.CommitSHA = &commit
	}
	if version.ActivatedAt != nil {
		activated := version.ActivatedAt.UTC().Format(time.RFC3339)
		mapped.ActivatedAt = &activated
	}
	if version.LatestVerification != nil {
		latest := mapPreventionVerification(*version.LatestVerification)
		mapped.LatestVerification = &latest
	}
	return mapped
}

func mapPreventionVerification(verification database.PreventionVerification) contract.PreventionVerification {
	mapped := contract.PreventionVerification{
		VerificationID: verification.VerificationID, ArtifactVersionID: verification.ArtifactVersionID,
		Command: append([]string{}, verification.Command...), Passed: verification.Passed,
		DurationMS: verification.DurationMS, Evidence: verification.Evidence,
		Environment: map[string]string{}, ContentSHA256: verification.ContentSHA256,
		CreatedAt: verification.CreatedAt.UTC().Format(time.RFC3339),
	}
	if verification.ExitCode != nil {
		code := *verification.ExitCode
		mapped.ExitCode = &code
	}
	for key, value := range verification.Environment {
		mapped.Environment[key] = value
	}
	return mapped
}
