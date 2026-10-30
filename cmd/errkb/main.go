//go:build windows

// Command errkb is the errkb toolbox: it captures failures from LLM tools,
// records a diagnosis once, and hands the known fix back the next time the same
// failure appears.
//
// This file is the EKB-030 subcommand dispatcher. The six CLI commands live in
// cli.go, the daemon in daemon.go. The binary targets Windows because the local
// API is a Windows Named Pipe (EKB-029) and the daemon lock is a Windows
// mutex (EKB-028); a Linux build would have nothing to talk to.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// dialClient can be replaced in tests; production talks to the real pipe.
var dialClient = func(ctx context.Context) (apiClient, error) {
	return service.Dial(ctx)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usage prints the command list. --help writes it to stdout, mistakes to stderr.
func usage(w io.Writer) {
	fmt.Fprintf(w, `errkb %s %s/%s

Usage: errkb <command> [flags] [--json]

Commands:
  search <query>         search the knowledge base for an error
  show <issue|finger>    explain one known issue
  record                 submit one ErrorEvent JSON (stdin or --event FILE)
  list                   list issues or solutions
  feedback               record an outcome for a solution version
  stats                  knowledge-base totals and timings
  prevention add|verify|activate|list|show
                         register, verify, activate and inspect preventions
  run -- <command>       run a command and look up its known fix
  tail --file <path>     follow a log file and record its failures
  markdown import|export exchange the legacy ERRORS.md format
  db migrate|status      create or inspect the database schema
  pg install|start|stop|uninstall|status|discover
                         manage the local PostgreSQL cluster
  mcp                    serve MCP over stdio for LLM hosts
  mcp install            register the MCP server with a local host (codex|claude|dsh)
  mcp selftest           answer one initialize and tools/list, then exit
  daemon                 run the local daemon
  doctor                 diagnose the runtime and connector state
  ui                     show the tray icon and capture state
  version                print the version

Global:
  --json                 print the contract JSON instead of the human form

Exit codes:
  0 found / success
  1 internal error
  2 bad arguments
  3 not found
  4 the daemon is not running
`, version, runtime.GOOS, runtime.GOARCH)
}

// run strips the global --json switch wherever it appears before a literal
// command separator. Arguments after `--` belong to the child command and must
// reach verification unchanged, including a child flag named --json.
func run(args []string, stdout, stderr io.Writer) int {
	jsonMode := false
	literalCommand := false
	clean := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--" {
			literalCommand = true
			clean = append(clean, arg)
			continue
		}
		if !literalCommand && arg == "--json" {
			jsonMode = true
			continue
		}
		clean = append(clean, arg)
	}
	if len(clean) == 0 {
		usage(stderr)
		return exitUsage
	}
	command, rest := clean[0], clean[1:]
	switch command {
	case "help", "--help", "-h":
		usage(stdout)
		return exitOK
	case "version", "--version", "-v":
		printVersion(stdout)
		return exitOK
	case "daemon":
		return runDaemon(rest)
	case "ui":
		return runUI(rest)
	case "doctor":
		return runDoctor(rest, jsonMode, stdout, stderr)
	case "db":
		return runDB(rest, jsonMode, stdout, stderr)
	case "pg":
		return runPG(rest, stdout, stderr)
	case "markdown":
		return runMarkdown(rest, jsonMode, stdout, stderr)
	case "tail":
		return runTail(rest, stdout, stderr)
	case "run":
		return runRun(rest, jsonMode, stdout, stderr, os.Stdin, &cliEnv{
			json:   jsonMode,
			stdout: stdout,
			stderr: stderr,
			stdin:  os.Stdin,
			dial:   dialClient,
		})
	case "mcp":
		return runMCP(rest, os.Stdin, stdout, stderr)
	}
	env := &cliEnv{
		json:   jsonMode,
		stdout: stdout,
		stderr: stderr,
		stdin:  os.Stdin,
		dial:   dialClient,
	}
	return runCommand(command, rest, env)
}
