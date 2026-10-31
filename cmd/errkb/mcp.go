//go:build windows

// The errkb mcp subcommand (EKB-037): MCP over stdio for hosts that speak the
// protocol. stdout carries protocol frames only; every diagnostic goes to
// stderr because one stray byte on stdout breaks the client's message stream.
//
// `errkb mcp` also makes the database usable without a separately managed
// daemon: it resolves the installed credentials/migrations and starts the
// daemon detached when the pipe is not reachable (unless --no-daemon). That
// keeps the MCP path at "register once, works from the next request".
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/mcp"
)

// pipeBackend answers the MCP tools over the local daemon pipe. It dials per
// call: a long-lived MCP session must survive a daemon restart, and the pipe
// client closes with each call, which is also how the CLI talks.
type pipeBackend struct {
	dial func(context.Context) (apiClient, error)
}

var _ mcp.Backend = pipeBackend{}

func (b pipeBackend) with(ctx context.Context, call func(context.Context, apiClient) error) error {
	client, err := b.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return call(ctx, client)
}

func (b pipeBackend) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	var response contract.LookupResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Lookup(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	var response contract.SearchResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Search(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	var response contract.RecordResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Record(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	var response contract.FeedbackResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Feedback(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	var response contract.ListResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.List(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	var response contract.StatsResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.Stats(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) RecordSolution(ctx context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	var response contract.RecordSolutionResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.RecordSolution(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) SetStatus(ctx context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	var response contract.SetStatusResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.SetStatus(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) GetIssue(ctx context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	var response contract.GetIssueResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.GetIssue(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) PreventionAdd(ctx context.Context, request contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	var response contract.PreventionAddResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.PreventionAdd(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) PreventionList(ctx context.Context, request contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	var response contract.PreventionListResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.PreventionList(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

func (b pipeBackend) PreventionGet(ctx context.Context, request contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	var response contract.PreventionGetResponse
	err := b.with(ctx, func(ctx context.Context, client apiClient) error {
		answer, callErr := client.PreventionGet(ctx, request)
		if callErr != nil {
			return callErr
		}
		response = answer
		return nil
	})
	return response, err
}

// runMCP dispatches the three MCP-related commands: serve (default),
// `install` (register with a known host) and `selftest` (in-process check).
func runMCP(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return runMCPInstall(args[1:], stdout, stderr)
		case "selftest":
			return runMCPSelftest(stderr)
		}
	}
	return runMCPServe(args, stdin, stdout, stderr)
}

// runMCPServe serves MCP over stdio, starting the daemon when necessary.
func runMCPServe(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: errkb mcp [--dsn DSN | --credentials FILE] [--migrations DIR] [--pipe NAME] [--no-daemon]")
		fmt.Fprintln(stderr, "       errkb mcp install [--host codex|claude|dsh|all] [--print] [--json]")
		fmt.Fprintln(stderr, "       errkb mcp selftest")
	}
	dsnFlag := flags.String("dsn", "", "database DSN (defaults to the installed pg\\credentials.json)")
	credentialsFlag := flags.String("credentials", "", "credentials.json written by `errkb pg install`")
	migrationsFlag := flags.String("migrations", "", "migrations directory")
	pipeFlag := flags.String("pipe", "", "Named Pipe name, for example errkb")
	noDaemon := flags.Bool("no-daemon", false, "do not start the daemon; return unavailable errors instead")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	dsn, migrations, err := resolveDaemonPaths(*dsnFlag, *credentialsFlag, *migrationsFlag)
	if err != nil {
		fmt.Fprintf(stderr, "errkb mcp: %v\n", err)
		return exitUsage
	}
	pipeName := ""
	if strings.TrimSpace(*pipeFlag) != "" {
		pipeName = pipeNameValue(*pipeFlag)
		_ = os.Setenv("ERRKB_PIPE", pipeName)
	}
	if !*noDaemon && dsn != "" {
		// Reuse a running daemon or start one detached. Failures are not fatal:
		// the host keeps its MCP session and each tool call reports unavailable.
		if _, daemonErr := ensureDaemon(context.Background(), dsn, migrations, pipeName, stderr, "errkb mcp"); daemonErr != nil {
			fmt.Fprintf(stderr, "errkb mcp: %v\n", daemonErr)
		}
	}
	server := mcp.NewServer(stdin, stdout, stderr, pipeBackend{dial: dialClient}, version)
	if serveErr := server.Serve(context.Background()); serveErr != nil {
		fmt.Fprintf(stderr, "errkb mcp: %v\n", serveErr)
		return exitInternal
	}
	return exitOK
}

// pipeNameValue turns "errkb" into the full pipe path the client expects.
func pipeNameValue(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, `\\.\pipe\`) {
		return name
	}
	return `\\.\pipe\` + name
}

// mcpInstallResult reports what `errkb mcp install` did for one host.
type mcpInstallResult struct {
	Host    string `json:"host"`
	Command string `json:"command,omitempty"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
}

// runMCPInstall registers this binary with a host that speaks MCP. Only hosts
// whose registration interface was measured are automated; anything else is
// printed for the operator instead of writing an unverified configuration.
func runMCPInstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	hostFlag := flags.String("host", "", "codex, claude, dsh, or all")
	printOnly := flags.Bool("print", false, "print the commands instead of running them")
	jsonMode := flags.Bool("json", false, "print machine-readable results")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: errkb mcp install --host codex|claude|dsh|all [--print] [--json]")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if strings.TrimSpace(*hostFlag) == "" {
		flags.Usage()
		return exitUsage
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "errkb mcp install: %v\n", err)
		return exitInternal
	}

	hosts := []string{*hostFlag}
	if *hostFlag == "all" {
		hosts = []string{"codex", "claude", "dsh"}
	}
	results := make([]mcpInstallResult, 0, len(hosts))
	failed := false
	for _, host := range hosts {
		result := installMCPHost(host, executable, *printOnly)
		if result.Status == "failed" {
			failed = true
		}
		results = append(results, result)
	}

	if *jsonMode {
		encoded, _ := json.MarshalIndent(results, "", "  ")
		fmt.Fprintln(stdout, string(encoded))
	} else {
		for _, result := range results {
			fmt.Fprintf(stdout, "%-6s %-10s %s\n", result.Host, result.Status, result.Detail)
		}
	}
	if failed {
		return exitInternal
	}
	return exitOK
}

// installMCPHost performs (or prints) the registration step for one host.
func installMCPHost(host, executable string, printOnly bool) mcpInstallResult {
	result := mcpInstallResult{Host: host}
	var command []string
	var remove []string
	switch host {
	case "codex":
		command = []string{"mcp", "add", "errkb", "--", executable, "mcp"}
		remove = []string{"mcp", "remove", "errkb"}
	case "claude":
		command = []string{"mcp", "add", "--scope", "user", "--transport", "stdio", "errkb", "--", executable, "mcp"}
		remove = []string{"mcp", "remove", "--scope", "user", "errkb"}
	case "dsh":
		result.Status = "manual"
		result.Detail = "DSH integrates through the dsh-errkb plugin (cordis.patch.yml); no MCP registration is needed"
		return result
	default:
		result.Status = "failed"
		result.Detail = fmt.Sprintf("unknown host %q (use codex, claude, dsh, or all)", host)
		return result
	}
	result.Command = host + " " + strings.Join(command, " ")
	if printOnly {
		result.Status = "printed"
		result.Detail = result.Command
		return result
	}
	if _, lookupErr := exec.LookPath(host); lookupErr != nil {
		result.Status = "failed"
		result.Detail = fmt.Sprintf("%s is not on PATH; run this on a machine with %s installed, or pass --print", host, host)
		return result
	}
	output, runErr := runHostCommand(host, command)
	if runErr != nil && strings.Contains(strings.ToLower(output), "already exists") && len(remove) > 0 {
		// Re-registering after an upgrade must replace the old entry instead of
		// failing with "already exists".
		_, _ = runHostCommand(host, remove)
		output, runErr = runHostCommand(host, command)
	}
	if runErr != nil {
		result.Status = "failed"
		result.Detail = strings.TrimSpace(output)
		if result.Detail == "" {
			result.Detail = runErr.Error()
		}
		return result
	}
	result.Status = "registered"
	result.Detail = strings.TrimSpace(output)
	return result
}

func runHostCommand(host string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, host, args...).CombinedOutput()
	return string(output), err
}

// rpcEnvelope is the subset of JSON-RPC 2.0 the selftest checks.
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *float64        `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// runMCPSelftest answers one initialize and one tools/list through the real
// server, in memory, so the wiring can be checked without a host or daemon.
// That is what `errkb doctor` uses to prove the MCP surface is intact.
func runMCPSelftest(stderr io.Writer) int {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	server := mcp.NewServer(inReader, outWriter, stderr, pipeBackend{dial: dialClient}, version)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	served := make(chan error, 1)
	go func() { served <- server.Serve(ctx) }()

	scanner := bufio.NewScanner(outReader)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	readResult := func(id float64) (json.RawMessage, error) {
		for scanner.Scan() {
			var envelope rpcEnvelope
			if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
				continue
			}
			if envelope.ID == nil || *envelope.ID != id {
				continue
			}
			if envelope.Error != nil {
				return nil, fmt.Errorf("%s", envelope.Error.Message)
			}
			return envelope.Result, nil
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("the server closed before answering id %v", id)
	}
	send := func(frame string) error {
		_, err := io.WriteString(inWriter, frame+"\n")
		return err
	}

	if err := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"errkb-selftest","version":"1"}}}`); err != nil {
		fmt.Fprintf(stderr, "errkb mcp selftest: %v\n", err)
		return exitInternal
	}
	if _, err := readResult(1); err != nil {
		fmt.Fprintf(stderr, "errkb mcp selftest: initialize: %v\n", err)
		return exitInternal
	}
	if err := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); err != nil {
		fmt.Fprintf(stderr, "errkb mcp selftest: %v\n", err)
		return exitInternal
	}
	result, err := readResult(2)
	if err != nil {
		fmt.Fprintf(stderr, "errkb mcp selftest: tools/list: %v\n", err)
		return exitInternal
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		fmt.Fprintf(stderr, "errkb mcp selftest: decode tools: %v\n", err)
		return exitInternal
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	for _, required := range []string{
		"err_lookup", "err_search", "err_record", "err_feedback", "err_list", "err_stats",
		"err_record_solution", "err_set_status", "err_get_issue",
		"err_prevention_register", "err_prevention_list", "err_prevention_get",
	} {
		found := false
		for _, name := range names {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(stderr, "errkb mcp selftest: tool %s is missing; got %v\n", required, names)
			return exitInternal
		}
	}
	_ = inWriter.Close()
	fmt.Fprintf(stderr, "mcp selftest: %d tools available\n", len(names))
	return exitOK
}
