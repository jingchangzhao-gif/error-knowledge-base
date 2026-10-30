//go:build windows

// The prevention CLI is the only user-facing surface that can execute a
// verification or activate a version. MCP and Web intentionally do not call
// these commands; artifact bodies remain in the user's Git repository.
package main

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

const preventionVerifyPipeTimeout = 125 * time.Second

func runPrevention(args []string, env *cliEnv) int {
	if len(args) == 0 {
		fmt.Fprintln(env.stderr, "errkb prevention: add, verify, activate, list or show is required")
		return exitUsage
	}
	switch args[0] {
	case "add":
		return runPreventionAdd(args[1:], env)
	case "verify":
		return runPreventionVerify(args[1:], env)
	case "activate":
		return runPreventionActivate(args[1:], env)
	case "list":
		return runPreventionList(args[1:], env)
	case "show":
		return runPreventionShow(args[1:], env)
	default:
		fmt.Fprintf(env.stderr, "errkb prevention: unknown subcommand %q\n", args[0])
		return exitUsage
	}
}

func runPreventionAdd(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("prevention add", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	issue := flags.String("issue", "", "resolved issue id (required)")
	kind := flags.String("kind", "", "regression_test, deterministic_check, agent_instruction or intent (required)")
	title := flags.String("title", "", "prevention artifact title (required)")
	repo := flags.String("repo", "", "repository path used only to hash the artifact (required)")
	path := flags.String("path", "", "repository-relative artifact path (required)")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 0 {
		return env.usageError("prevention add", fmt.Errorf("it takes no positional arguments"))
	}
	if strings.TrimSpace(*issue) == "" || strings.TrimSpace(*kind) == "" || strings.TrimSpace(*title) == "" || strings.TrimSpace(*repo) == "" || strings.TrimSpace(*path) == "" {
		return env.usageError("prevention add", fmt.Errorf("--issue, --kind, --title, --repo and --path are required"))
	}
	hash, err := service.HashPreventionArtifact(*repo, *path)
	if err != nil {
		return env.fail("prevention add", err)
	}
	request := contract.PreventionAddRequest{
		SchemaVersion: contract.PreventionSchemaVersion, IssueID: *issue, Kind: *kind, Title: *title,
		ArtifactPath: *path, ContentSHA256: hash, Source: "cli", Actor: "cli",
	}
	if err := request.Validate(); err != nil {
		return env.usageError("prevention add", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.PreventionAdd(ctx, request)
	if err != nil {
		return env.fail("prevention add", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	fmt.Fprintf(env.stdout, "artifact %s\n", response.Artifact.ArtifactID)
	fmt.Fprintf(env.stdout, "version %s  number %d  status %s\n", response.Version.ArtifactVersionID, response.Version.Version, response.Version.Status)
	fmt.Fprintf(env.stdout, "created=%t  path %s  sha256 %s\n", response.Created, response.Version.ArtifactPath, response.Version.ContentSHA256)
	fmt.Fprintf(env.stdout, "next: errkb prevention verify %s --repo <repository-path> -- <command> [args...]\n", response.Version.ArtifactVersionID)
	return exitOK
}

func runPreventionVerify(args []string, env *cliEnv) int {
	flagArgs, command, err := splitRunArgs(args)
	if err != nil {
		return env.usageError("prevention verify", err)
	}
	flags := flag.NewFlagSet("prevention verify", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	repo := flags.String("repo", "", "repository path (required)")
	positionals, positional := splitCommandArguments(flagArgs)
	if err := flags.Parse(positionals); err != nil {
		return exitUsage
	}
	if len(positional) != 1 || strings.TrimSpace(*repo) == "" {
		return env.usageError("prevention verify", fmt.Errorf("exactly one version id and --repo are required before `--`"))
	}
	request := contract.PreventionVerifyRequest{
		SchemaVersion: contract.PreventionSchemaVersion, ArtifactVersionID: positional[0],
		RepoPath: *repo, Command: append([]string(nil), command...),
	}
	if err := request.Validate(); err != nil {
		return env.usageError("prevention verify", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContextWithTimeout(preventionVerifyPipeTimeout)
	defer cancel()
	response, err := client.PreventionVerify(ctx, request)
	if err != nil {
		return env.fail("prevention verify", err)
	}
	if env.json {
		if code := env.writeJSON(response); code != exitOK {
			return code
		}
	} else {
		renderPreventionVerification(env, response.Verification)
		fmt.Fprintf(env.stdout, "version %s  status %s\n", response.Version.ArtifactVersionID, response.Version.Status)
		if response.Verification.Passed {
			fmt.Fprintf(env.stdout, "next: errkb prevention activate %s --repo <repository-path>\n", response.Version.ArtifactVersionID)
		}
	}
	if !response.Verification.Passed {
		return exitInternal
	}
	return exitOK
}

func runPreventionActivate(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("prevention activate", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	repo := flags.String("repo", "", "repository path (required)")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 1 || strings.TrimSpace(*repo) == "" {
		return env.usageError("prevention activate", fmt.Errorf("exactly one version id and --repo are required"))
	}
	request := contract.PreventionActivateRequest{SchemaVersion: contract.PreventionSchemaVersion, ArtifactVersionID: positional[0], RepoPath: *repo}
	if err := request.Validate(); err != nil {
		return env.usageError("prevention activate", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.PreventionActivate(ctx, request)
	if err != nil {
		return env.fail("prevention activate", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	fmt.Fprintf(env.stdout, "version %s  status %s  activated=%t\n", response.Version.ArtifactVersionID, response.Version.Status, response.Activated)
	if response.Version.CommitSHA != nil {
		fmt.Fprintf(env.stdout, "commit %s\n", *response.Version.CommitSHA)
	}
	if response.Version.ActivatedAt != nil {
		fmt.Fprintf(env.stdout, "activated_at %s\n", *response.Version.ActivatedAt)
	}
	return exitOK
}

func runPreventionList(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("prevention list", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	issue := flags.String("issue", "", "filter by issue id")
	project := flags.String("project", "", "filter by project key")
	kind := flags.String("kind", "", "filter by prevention kind")
	status := flags.String("status", "", "filter by version status")
	limit := flags.Int("limit", 0, "page size (1..100)")
	cursor := flags.String("cursor", "", "continue after this cursor")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 0 {
		return env.usageError("prevention list", fmt.Errorf("it takes no positional arguments"))
	}
	request := contract.PreventionListRequest{
		SchemaVersion: contract.PreventionSchemaVersion, IssueID: *issue, ProjectKey: *project,
		Kind: *kind, Status: *status, Cursor: *cursor,
	}
	if *limit != 0 {
		value := *limit
		request.Limit = &value
	}
	if err := request.Validate(); err != nil {
		return env.usageError("prevention list", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.PreventionList(ctx, request)
	if err != nil {
		return env.fail("prevention list", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	if len(response.Items) == 0 {
		fmt.Fprintln(env.stdout, "no preventions")
	}
	for _, item := range response.Items {
		fmt.Fprintf(env.stdout, "artifact %s  %s  %s\n", item.Artifact.ArtifactID, item.Artifact.Kind, item.Artifact.Title)
		if latest, ok := latestPreventionVersion(item.Versions); ok {
			fmt.Fprintf(env.stdout, "   latest version %s  number %d  status %s\n", latest.ArtifactVersionID, latest.Version, latest.Status)
		}
	}
	if response.NextCursor != "" {
		fmt.Fprintf(env.stdout, "next cursor: %s\n", response.NextCursor)
	}
	return exitOK
}

func runPreventionShow(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("prevention show", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 1 {
		return env.usageError("prevention show", fmt.Errorf("exactly one artifact id is required"))
	}
	request := contract.PreventionGetRequest{SchemaVersion: contract.PreventionSchemaVersion, ArtifactID: positional[0]}
	if err := request.Validate(); err != nil {
		return env.usageError("prevention show", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.PreventionGet(ctx, request)
	if err != nil {
		return env.fail("prevention show", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	fmt.Fprintf(env.stdout, "artifact %s  %s  %s\n", response.Artifact.ArtifactID, response.Artifact.Kind, response.Artifact.Title)
	fmt.Fprintf(env.stdout, "project %s  issues %s\n", response.Artifact.ProjectKey, strings.Join(response.Artifact.IssueIDs, ","))
	for _, version := range response.Versions {
		fmt.Fprintf(env.stdout, "version %s  number %d  status %s\n", version.ArtifactVersionID, version.Version, version.Status)
		fmt.Fprintf(env.stdout, "   path %s  sha256 %s\n", version.ArtifactPath, version.ContentSHA256)
		if version.CommitSHA != nil {
			fmt.Fprintf(env.stdout, "   commit %s\n", *version.CommitSHA)
		}
		if version.ActivatedAt != nil {
			fmt.Fprintf(env.stdout, "   activated_at %s\n", *version.ActivatedAt)
		}
		if version.LatestVerification != nil {
			renderPreventionVerification(env, *version.LatestVerification)
		}
		switch version.Status {
		case "draft":
			fmt.Fprintf(env.stdout, "   next: errkb prevention verify %s --repo <repository-path> -- <command> [args...]\n", version.ArtifactVersionID)
		case "verified":
			fmt.Fprintf(env.stdout, "   next: errkb prevention activate %s --repo <repository-path>\n", version.ArtifactVersionID)
		}
	}
	return exitOK
}

func latestPreventionVersion(versions []contract.PreventionVersion) (contract.PreventionVersion, bool) {
	if len(versions) == 0 {
		return contract.PreventionVersion{}, false
	}
	latest := versions[0]
	for _, version := range versions[1:] {
		if version.Version > latest.Version {
			latest = version
		}
	}
	return latest, true
}

func renderPreventionVerification(env *cliEnv, verification contract.PreventionVerification) {
	fmt.Fprintf(env.stdout, "verification %s  passed=%t  duration_ms=%d\n", verification.VerificationID, verification.Passed, verification.DurationMS)
	if verification.ExitCode != nil {
		fmt.Fprintf(env.stdout, "   exit=%d\n", *verification.ExitCode)
	} else {
		fmt.Fprintln(env.stdout, "   exit=none")
	}
	if verification.Evidence != "" {
		fmt.Fprintf(env.stdout, "   evidence:\n%s\n", verification.Evidence)
	}
}
