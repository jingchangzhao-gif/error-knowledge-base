//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func runDBForTest(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runDB(args, false, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestDBRejectsBadUsage(t *testing.T) {
	cases := map[string][]string{
		"no subcommand":   {},
		"unknown":         {"vacuum"},
		"unexpected args": {"status", "extra"},
		"no connection":   {"status"},
	}
	want := map[string]string{
		"no subcommand":   "migrate or status",
		"unknown":         "unknown subcommand",
		"unexpected args": "unexpected argument",
		"no connection":   "pass --dsn or --credentials",
	}
	for name, args := range cases {
		code, _, stderr := runDBForTest(t, args...)
		if code != exitUsage {
			t.Errorf("%s: exit = %d, want %d (stderr %q)", name, code, exitUsage, stderr)
		}
		if !strings.Contains(stderr, want[name]) {
			t.Errorf("%s: stderr %q does not contain %q", name, stderr, want[name])
		}
	}
}

func TestDBHelpAndMissingMigrations(t *testing.T) {
	if code, out, _ := runDBForTest(t, "help"); code != exitOK || !strings.Contains(out, "db migrate|status") {
		t.Fatalf("help: code=%d out=%q", code, out)
	}
	code, _, stderr := runDBForTest(t, "migrate", "--dsn", "postgres://u:p@127.0.0.1:1/x", "--migrations", filepath.Join(t.TempDir(), "missing"))
	if code != exitInternal {
		t.Fatalf("missing migrations dir: exit = %d, want %d (stderr %q)", code, exitInternal, stderr)
	}
	if !strings.Contains(stderr, "no migrations") && !strings.Contains(stderr, "migrations") {
		t.Fatalf("missing migrations dir: stderr %q does not explain the directory", stderr)
	}
}

func TestResolveDBTargetRedactsThePassword(t *testing.T) {
	target, err := resolveDBTarget("postgres://user:secret@127.0.0.1:5433/errkb?sslmode=disable", "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Contains(target.label, "secret") {
		t.Fatalf("label leaks the password: %q", target.label)
	}
	if !strings.Contains(target.label, "user@127.0.0.1:5433") {
		t.Fatalf("label = %q, want the user and address", target.label)
	}
	if target.dsn == "" || !strings.Contains(target.dsn, "secret") {
		t.Fatalf("the DSN must keep the password for the connection itself")
	}
}

func TestResolveDBTargetReadsCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	document := `{"version":1,"port":5433,"database":"errkb","superuser":"postgres","password":"pa-ss","data_dir":"C:\\app\\pg\\data"}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := resolveDBTarget("", path)
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if !strings.HasPrefix(target.dsn, "postgres://postgres:pa-ss@127.0.0.1:5433/errkb") {
		t.Fatalf("dsn = %q", target.dsn)
	}
	if strings.Contains(target.label, "pa-ss") {
		t.Fatalf("label leaks the password: %q", target.label)
	}
	if _, err := resolveDBTarget("", filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("an unreadable credentials file must be an error")
	}
}

func TestRedactDSNFallback(t *testing.T) {
	if got := redactDSN("not a url"); got != "(connection string)" {
		t.Fatalf("redactDSN(%q) = %q", "not a url", got)
	}
	if got := redactDSN("postgres://u@h/db"); got != "postgres://u@h/db" {
		t.Fatalf("redactDSN without a password = %q", got)
	}
}

func TestSplitRunArgs(t *testing.T) {
	flags, command, err := splitRunArgs([]string{"--", "cmd", "/c", "exit", "3"})
	if err != nil {
		t.Fatalf("bare separator: %v", err)
	}
	if len(flags) != 0 || strings.Join(command, " ") != "cmd /c exit 3" {
		t.Fatalf("bare separator: flags=%v command=%v", flags, command)
	}

	flags, command, err = splitRunArgs([]string{"--connector", "c", "--session", "s", "--", "pnpm", "build"})
	if err != nil {
		t.Fatalf("flags: %v", err)
	}
	if strings.Join(flags, " ") != "--connector c --session s" || strings.Join(command, " ") != "pnpm build" {
		t.Fatalf("flags: flags=%v command=%v", flags, command)
	}

	if _, _, err := splitRunArgs([]string{"pnpm", "build"}); err == nil {
		t.Fatal("a missing separator must be an error")
	}
	if _, _, err := splitRunArgs([]string{"--connector", "c", "--"}); err == nil {
		t.Fatal("an empty command must be an error")
	}
	if _, _, err := splitRunArgs(nil); err == nil {
		t.Fatal("nil arguments must be an error")
	}
}

// TestDBMigrateCreatesTheSchema runs the real chain against a throwaway
// database. It needs a PostgreSQL server, exactly like the other database tests.
func TestDBMigrateCreatesTheSchema(t *testing.T) {
	adminDSN := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if adminDSN == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the database test")
	}
	migrationsDir := filepath.Join("..", "..", "db", "migrations")
	if _, err := os.Stat(migrationsDir); err != nil {
		t.Fatalf("migrations dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	name := fmt.Sprintf("errkb_dbcmd_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	dsn := databaseName(adminDSN, name)
	defer func() {
		_, _ = admin.Exec(context.Background(), "drop database if exists "+name+" with (force)")
		_ = admin.Close(context.Background())
	}()

	code, stdout, stderr := runDBForTest(t, "migrate", "--dsn", dsn, "--migrations", migrationsDir)
	if code != exitOK {
		t.Fatalf("first migrate: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "applied") {
		t.Fatalf("first migrate: stdout %q does not report the applied migrations", stdout)
	}

	code, stdout, _ = runDBForTest(t, "migrate", "--dsn", dsn, "--migrations", migrationsDir)
	if code != exitOK || !strings.Contains(stdout, "up to date") {
		t.Fatalf("second migrate: exit %d, stdout %q", code, stdout)
	}

	var out, errOut bytes.Buffer
	if code := runDB([]string{"status", "--dsn", dsn, "--migrations", migrationsDir}, true, &out, &errOut); code != exitOK {
		t.Fatalf("status: exit %d, stderr %q", code, errOut.String())
	}
	var report dbReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("status json: %v (%q)", err, out.String())
	}
	if !report.UpToDate || len(report.Applied) != 9 || len(report.Pending) != 0 {
		t.Fatalf("status report = %+v", report)
	}
	if strings.Contains(report.Target, "secret") {
		t.Fatalf("target leaks a password: %q", report.Target)
	}
}

// databaseName replaces the database part of a PostgreSQL URL.
func databaseName(dsn, name string) string {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	parsed.Path = "/" + name
	return parsed.String()
}
