//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/mcp"
)

// TestMCPSelftestAnswers runs the same initialize + tools/list exchange a host
// performs, in memory, and checks the twelve-tool summary and human-only CLI
// prevention transitions.
func TestMCPSelftestAnswers(t *testing.T) {
	var stderr bytes.Buffer
	if code := runMCP([]string{"selftest"}, strings.NewReader(""), io.Discard, &stderr); code != exitOK {
		t.Fatalf("selftest exited %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "12 tools available") {
		t.Fatalf("selftest summary does not report the tool count: %q", stderr.String())
	}
	notice := strings.ToLower(mcp.CorrectionNotice)
	for _, wanted := range []string{"prevention", "draft", "verify", "activate", "cli", "only"} {
		if !strings.Contains(notice, wanted) {
			t.Fatalf("MCP instructions do not state the CLI boundary %q: %s", wanted, mcp.CorrectionNotice)
		}
	}
}

// TestMCPInstallPrintAllHosts checks the dry-run registration output: codex and
// claude print their measured command lines, DSH is manual by design.
func TestMCPInstallPrintAllHosts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMCP([]string{"install", "--host", "all", "--print", "--json"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("install exited %d: %s", code, stderr.String())
	}
	var results []mcpInstallResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("install output is not JSON: %v\n%s", err, stdout.String())
	}
	if len(results) != 3 {
		t.Fatalf("install reported %d hosts, want 3: %+v", len(results), results)
	}
	byHost := map[string]mcpInstallResult{}
	for _, result := range results {
		byHost[result.Host] = result
	}
	codex := byHost["codex"]
	if codex.Status != "printed" || !strings.Contains(codex.Command, "mcp add errkb --") {
		t.Fatalf("codex registration is not the measured command: %+v", codex)
	}
	claude := byHost["claude"]
	if claude.Status != "printed" || !strings.Contains(claude.Command, "--scope user") {
		t.Fatalf("claude registration is not the measured command: %+v", claude)
	}
	if byHost["dsh"].Status != "manual" {
		t.Fatalf("dsh must stay manual (plugin integration): %+v", byHost["dsh"])
	}
}

// TestMCPInstallDshIsManual keeps the plugin-based integration explicit rather
// than writing a configuration DSH does not read.
func TestMCPInstallDshIsManual(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMCP([]string{"install", "--host", "dsh", "--json"}, strings.NewReader(""), &stdout, &stderr); code != exitOK {
		t.Fatalf("dsh install exited %d: %s", code, stderr.String())
	}
	var results []mcpInstallResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("dsh install output is not JSON: %v", err)
	}
	if len(results) != 1 || results[0].Status != "manual" || results[0].Command != "" {
		t.Fatalf("dsh install must not invent a command: %+v", results)
	}
	if !strings.Contains(results[0].Detail, "plugin") {
		t.Fatalf("dsh detail does not name the integration path: %+v", results[0])
	}
}

// TestMCPInstallRejectsUnknownHost fails loudly instead of guessing a config.
func TestMCPInstallRejectsUnknownHost(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMCP([]string{"install", "--host", "unknown-host", "--json"}, strings.NewReader(""), &stdout, &stderr); code != exitInternal {
		t.Fatalf("unknown host exited %d, want %d", code, exitInternal)
	}
}

// TestMCPInstallNeedsHost keeps the flag mandatory.
func TestMCPInstallNeedsHost(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMCP([]string{"install"}, strings.NewReader(""), &stdout, &stderr); code != exitUsage {
		t.Fatalf("missing --host exited %d, want %d", code, exitUsage)
	}
}

// TestMCPHelp prints the usage on stderr and succeeds.
func TestMCPHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMCP([]string{"--help"}, strings.NewReader(""), &stdout, &stderr); code != exitOK {
		t.Fatalf("help exited %d", code)
	}
	if !strings.Contains(stderr.String(), "mcp install") {
		t.Fatalf("help does not mention the install helper: %q", stderr.String())
	}
}
