//go:build windows

// The six CLI commands (EKB-030). They render what the daemon answered and
// never touch PostgreSQL, never normalize text and never decide whether a
// match is good: that judgement is the core's job (EKB-013 to EKB-024).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// Exit codes are part of the CLI contract (card EKB-030): callers must be able
// to tell "found nothing" from "bad arguments" from "daemon not running".
const (
	exitOK          = 0
	exitInternal    = 1
	exitUsage       = 2
	exitNotFound    = 3
	exitUnavailable = 4
)

// cliTimeout bounds one pipe round trip so a stuck daemon cannot hang a shell.
const cliTimeout = 30 * time.Second

// apiClient is the part of the daemon client the commands use; tests substitute
// a fake through cliEnv.dial.
type apiClient interface {
	Lookup(context.Context, contract.LookupRequest) (contract.LookupResponse, error)
	Search(context.Context, contract.SearchRequest) (contract.SearchResponse, error)
	Record(context.Context, contract.RecordRequest) (contract.RecordResponse, error)
	Capture(context.Context, contract.CaptureRequest) (contract.CaptureResponse, error)
	Feedback(context.Context, contract.FeedbackRequest) (contract.FeedbackResponse, error)
	List(context.Context, contract.ListRequest) (contract.ListResponse, error)
	Stats(context.Context, contract.StatsRequest) (contract.StatsResponse, error)
	RecordSolution(context.Context, contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error)
	SetStatus(context.Context, contract.SetStatusRequest) (contract.SetStatusResponse, error)
	GetIssue(context.Context, contract.GetIssueRequest) (contract.GetIssueResponse, error)
	PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error)
	PreventionVerify(context.Context, contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error)
	PreventionActivate(context.Context, contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error)
	PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error)
	PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error)
	Close() error
}

// cliEnv carries everything a command needs to be testable without a daemon.
type cliEnv struct {
	json   bool
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	dial   func(context.Context) (apiClient, error)
}

func (env *cliEnv) open() (apiClient, int) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	client, err := env.dial(ctx)
	if err != nil {
		fmt.Fprintf(env.stderr, "errkb: cannot reach the daemon: %v\n", err)
		if errors.Is(err, service.ErrServiceUnavailable) {
			return nil, exitUnavailable
		}
		return nil, exitInternal
	}
	return client, exitOK
}

func (env *cliEnv) callContext() (context.Context, context.CancelFunc) {
	return env.callContextWithTimeout(cliTimeout)
}

func (env *cliEnv) callContextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// usageError reports a problem with the arguments, without a stack trace.
func (env *cliEnv) usageError(operation string, err error) int {
	fmt.Fprintf(env.stderr, "errkb %s: %v\n", operation, err)
	return exitUsage
}

// fail maps a daemon or transport error onto the documented exit codes.
func (env *cliEnv) fail(operation string, err error) int {
	var callErr *service.CallError
	if errors.As(err, &callErr) {
		fmt.Fprintf(env.stderr, "errkb %s: %s: %s\n", operation, callErr.Code, callErr.Message)
		switch callErr.Code {
		case service.CodeInvalidRequest:
			return exitUsage
		case service.CodeNotFound:
			return exitNotFound
		case service.CodeUnavailable:
			return exitUnavailable
		default:
			return exitInternal
		}
	}
	if errors.Is(err, service.ErrServiceUnavailable) {
		fmt.Fprintf(env.stderr, "errkb %s: %v\n", operation, err)
		return exitUnavailable
	}
	fmt.Fprintf(env.stderr, "errkb %s: %v\n", operation, err)
	return exitInternal
}

// writeJSON prints the contract-shaped answer; --json output is the struct the
// EKB-008 contract defines, so scripts never see invented fields.
func (env *cliEnv) writeJSON(value any) int {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(env.stderr, "errkb: encode the JSON output: %v\n", err)
		return exitInternal
	}
	fmt.Fprintln(env.stdout, string(encoded))
	return exitOK
}

// renderCandidates prints the ranking the core produced and explains why it is
// there. It never adds candidates, reorders them or turns one into a reuse.
func (env *cliEnv) renderCandidates(candidates []contract.Candidate, jsonValue any) int {
	if env.json {
		return env.writeJSON(jsonValue)
	}
	if len(candidates) == 0 {
		fmt.Fprintln(env.stdout, "no matching issue yet")
		return exitNotFound
	}
	for index, candidate := range candidates {
		fmt.Fprintf(env.stdout, "%d. %s\n", index+1, candidate.Title)
		fmt.Fprintf(env.stdout, "   issue %s  fingerprint %s\n", candidate.IssueID, candidate.Fingerprint)
		if candidate.Confidence != nil {
			fmt.Fprintf(env.stdout, "   confidence %.2f\n", *candidate.Confidence)
		}
		marker := "exact match"
		if candidate.Approximate {
			marker = "APPROXIMATE - confirm before reuse"
		}
		fmt.Fprintf(env.stdout, "   reason %s (%s)\n", candidate.MatchReason, marker)
		// The contract has no separate environment field (EKB-008): environment
		// differences travel inside match_detail.
		fmt.Fprintf(env.stdout, "   detail %s\n", candidate.MatchDetail)
		if candidate.Solution != nil {
			fmt.Fprintf(env.stdout, "   fix %s\n", candidate.Solution.Body)
			fmt.Fprintf(env.stdout, "   source %s  trust %.2f\n", candidate.Solution.Source, candidate.Solution.Trust)
			if candidate.Solution.AppliesTo != "" {
				fmt.Fprintf(env.stdout, "   applies to %s\n", candidate.Solution.AppliesTo)
			}
			fmt.Fprintf(env.stdout, "   version %s\n", candidate.Solution.SolutionVersionID)
		}
	}
	return exitOK
}

func runCommand(command string, args []string, env *cliEnv) int {
	switch command {
	case "search":
		return runSearch(args, env)
	case "show":
		return runShow(args, env)
	case "record":
		return runRecord(args, env)
	case "list":
		return runList(args, env)
	case "feedback":
		return runFeedback(args, env)
	case "stats":
		return runStats(args, env)
	case "prevention":
		return runPrevention(args, env)
	}
	fmt.Fprintf(env.stderr, "errkb: unknown command %q\n\n", command)
	usage(env.stderr)
	return exitUsage
}

// splitCommandArguments separates flags from positional arguments so a command
// accepts flags before or after its positionals (the flag package stops parsing
// at the first non-flag token). Every CLI flag takes a value, so the token that
// follows a flag belongs to it.
func splitCommandArguments(args []string) (flagArgs, positional []string) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if index+1 < len(args) {
			flagArgs = append(flagArgs, args[index+1])
			index++
		}
	}
	return flagArgs, positional
}

func runSearch(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	project := flags.String("project", "", "restrict the search to one project key")
	limit := flags.Int("limit", 0, "how many candidates to ask for (1..3)")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(env.stderr, "errkb search: exactly one query argument is required")
		return exitUsage
	}
	request := contract.SearchRequest{
		SchemaVersion: contract.ToolSchemaVersion,
		Query:         positional[0],
		ProjectKey:    *project,
	}
	if *limit != 0 {
		value := *limit
		request.Limit = &value
	}
	if err := request.Validate(); err != nil {
		return env.usageError("search", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.Search(ctx, request)
	if err != nil {
		return env.fail("search", err)
	}
	return env.renderCandidates(response.Candidates, response)
}

// looksLikeFingerprint reports whether the argument has the signature shape;
// anything else is treated as an issue id.
func looksLikeFingerprint(value string) bool {
	if len(value) != 12 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func runShow(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("show", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(env.stderr, "errkb show: exactly one issue id or fingerprint is required")
		return exitUsage
	}
	value := strings.TrimSpace(positional[0])
	request := contract.LookupRequest{SchemaVersion: contract.ToolSchemaVersion}
	if looksLikeFingerprint(value) {
		request.Fingerprint = value
	} else {
		request.Issue = value
	}
	if err := request.Validate(); err != nil {
		return env.usageError("show", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.Lookup(ctx, request)
	if err != nil {
		return env.fail("show", err)
	}
	return env.renderCandidates(response.Candidates, response)
}

func runRecord(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	eventPath := flags.String("event", "", "read the ErrorEvent JSON from this file instead of stdin")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 0 {
		fmt.Fprintln(env.stderr, "errkb record: it takes no positional arguments")
		return exitUsage
	}
	var (
		data []byte
		err  error
	)
	if *eventPath != "" {
		data, err = os.ReadFile(*eventPath)
	} else {
		data, err = io.ReadAll(io.LimitReader(env.stdin, 1<<20))
	}
	if err != nil {
		return env.usageError("record", fmt.Errorf("read the event JSON: %w", err))
	}
	event, err := contract.DecodeErrorEvent(data)
	if err != nil {
		return env.usageError("record", err)
	}
	request := contract.RecordRequest{SchemaVersion: contract.ToolSchemaVersion, Event: event}
	if err := request.Validate(); err != nil {
		return env.usageError("record", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.Record(ctx, request)
	if err != nil {
		return env.fail("record", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	created := "no"
	if response.Created {
		created = "yes"
	}
	fmt.Fprintf(env.stdout, "recorded event %s\n", response.EventID)
	if response.Queued {
		fmt.Fprintf(env.stdout, "   queued for replay (fingerprint %s)\n", response.Fingerprint)
		fmt.Fprintln(env.stdout, "   the database is unreachable; the daemon replays the redacted event when it recovers")
		return exitOK
	}
	fmt.Fprintf(env.stdout, "   occurrence %s\n", response.OccurrenceID)
	fmt.Fprintf(env.stdout, "   issue %s\n", response.IssueID)
	fmt.Fprintf(env.stdout, "   fingerprint %s\n", response.Fingerprint)
	fmt.Fprintf(env.stdout, "   new issue %s\n", created)
	return exitOK
}

func runFeedback(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("feedback", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	outcome := flags.String("outcome", "", "success, failure or unknown (required)")
	note := flags.String("note", "", "short note about how the fix behaved")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 2 {
		fmt.Fprintln(env.stderr, "errkb feedback: an issue id and a solution version id are required")
		return exitUsage
	}
	request := contract.FeedbackRequest{
		SchemaVersion:     contract.ToolSchemaVersion,
		IssueID:           positional[0],
		SolutionVersionID: positional[1],
		Outcome:           *outcome,
		Note:              *note,
	}
	if err := request.Validate(); err != nil {
		return env.usageError("feedback", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.Feedback(ctx, request)
	if err != nil {
		return env.fail("feedback", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	fmt.Fprintf(env.stdout, "recorded outcome %s\n", response.OutcomeID)
	return exitOK
}

func runList(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	kind := flags.String("kind", "issue", "list issues or solutions")
	project := flags.String("project", "", "restrict the list to one project key")
	limit := flags.Int("limit", 0, "page size (1..100)")
	cursor := flags.String("cursor", "", "continue after this cursor")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 0 {
		fmt.Fprintln(env.stderr, "errkb list: it takes no positional arguments")
		return exitUsage
	}
	request := contract.ListRequest{
		SchemaVersion: contract.ToolSchemaVersion,
		Kind:          *kind,
		ProjectKey:    *project,
		Cursor:        *cursor,
	}
	if *limit != 0 {
		value := *limit
		request.Limit = &value
	}
	if err := request.Validate(); err != nil {
		return env.usageError("list", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.List(ctx, request)
	if err != nil {
		return env.fail("list", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	if len(response.Items) == 0 {
		fmt.Fprintln(env.stdout, "no entries")
	} else {
		fmt.Fprintf(env.stdout, "%d %s(s)\n", len(response.Items), request.Kind)
		for _, item := range response.Items {
			fmt.Fprintf(env.stdout, "%s  %s  %s\n", item.ID, item.UpdatedAt, item.Title)
		}
	}
	if response.NextCursor != "" {
		fmt.Fprintf(env.stdout, "next cursor: %s\n", response.NextCursor)
	}
	return exitOK
}

func runStats(args []string, env *cliEnv) int {
	flags := flag.NewFlagSet("stats", flag.ContinueOnError)
	flags.SetOutput(env.stderr)
	project := flags.String("project", "", "restrict the totals to one project key")
	flagArgs, positional := splitCommandArguments(args)
	if err := flags.Parse(flagArgs); err != nil {
		return exitUsage
	}
	if len(positional) != 0 {
		fmt.Fprintln(env.stderr, "errkb stats: it takes no positional arguments")
		return exitUsage
	}
	request := contract.StatsRequest{SchemaVersion: contract.ToolSchemaVersion, ProjectKey: *project}
	if err := request.Validate(); err != nil {
		return env.usageError("stats", err)
	}
	client, code := env.open()
	if code != exitOK {
		return code
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := env.callContext()
	defer cancel()
	response, err := client.Stats(ctx, request)
	if err != nil {
		return env.fail("stats", err)
	}
	if env.json {
		return env.writeJSON(response)
	}
	fmt.Fprintf(env.stdout, "totals: events=%d issues=%d solutions=%d outcomes=%d\n",
		response.Totals.Events, response.Totals.Issues, response.Totals.Solutions, response.Totals.Outcomes)
	fmt.Fprintf(env.stdout, "retrieval: p50=%dms p95=%dms\n", response.Retrieval.P50MS, response.Retrieval.P95MS)
	fmt.Fprintf(env.stdout, "tokens: estimated_saved=%d measured_saved=%d measured_events=%d\n",
		response.Tokens.EstimatedSaved, response.Tokens.MeasuredSaved, response.Tokens.MeasuredEvents)
	return exitOK
}
