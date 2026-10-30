package capture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// TestRunHelper is the child process the wrapper tests drive. The test binary
// re-executes itself with ERRKB_RUN_HELPER set, which keeps the fixtures
// portable and independent of the shell.
func TestRunHelper(t *testing.T) {
	mode := os.Getenv("ERRKB_RUN_HELPER")
	if mode == "" {
		t.Skip("helper mode runs only as a subprocess")
	}
	switch mode {
	case "exit":
		os.Exit(helperInt("ERRKB_RUN_EXIT"))
	case "streams":
		fmt.Fprintln(os.Stdout, "helper stdout line")
		fmt.Fprintln(os.Stderr, "helper stderr line")
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "error: build failed after linking")
		os.Exit(1)
	case "flood":
		// 200 KB is far beyond the test capture limit; the wrapper must keep
		// only the head and the tail.
		chunk := strings.Repeat("x", 1024)
		for i := 0; i < 200; i++ {
			if _, err := io.WriteString(os.Stdout, chunk); err != nil {
				os.Exit(90)
			}
		}
		fmt.Fprintln(os.Stdout, " error: tail line after the flood")
		os.Exit(1)
	case "transient":
		fmt.Fprintln(os.Stderr, "dial tcp 127.0.0.1:5432: i/o timeout")
		os.Exit(1)
	case "secret":
		fmt.Fprintln(os.Stderr, "auth failed for "+"sk-"+"abcdefghijklmnopqrstuvwx"+" while calling the API")
		os.Exit(1)
	case "live":
		fmt.Fprintln(os.Stdout, "first-line")
		time.Sleep(400 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "second-line")
		os.Exit(0)
	case "stdin":
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			os.Exit(90)
		}
		os.Exit(0)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper mode "+mode)
		os.Exit(91)
	}
}

func helperInt(name string) int {
	value := 0
	if _, err := fmt.Sscanf(os.Getenv(name), "%d", &value); err != nil {
		return 1
	}
	return value
}

// helperOptions builds a wrapper configuration that runs the test binary in
// the requested helper mode.
func helperOptions(mode string, extraEnv ...string) capture.RunOptions {
	env := append(os.Environ(), "ERRKB_RUN_HELPER="+mode)
	env = append(env, extraEnv...)
	return capture.RunOptions{
		Command: []string{testExecutable(), "-test.run=^TestRunHelper$"},
		Env:     env,
	}
}

// testExecutable resolves the running test binary once; the helper process is
// this same binary re-executed.
func testExecutable() string {
	path, err := os.Executable()
	if err != nil {
		panic("the capture tests need the test binary path: " + err.Error())
	}
	return path
}

func TestRunPassesExitCodesUnchanged(t *testing.T) {
	for _, code := range []int{0, 1, 127} {
		options := helperOptions("exit", fmt.Sprintf("ERRKB_RUN_EXIT=%d", code))
		result, err := capture.Run(context.Background(), options)
		if err != nil {
			t.Fatalf("exit %d: %v", code, err)
		}
		if result.ExitCode != code {
			t.Fatalf("exit %d: wrapper reported %d", code, result.ExitCode)
		}
	}
}

func TestRunKeepsStreamsSeparateAndSkipsSuccess(t *testing.T) {
	var recorded int
	options := helperOptions("streams")
	options.ProjectKey = "capture-test"
	options.ConnectorID = "0195f0f0-0000-4000-8000-00000000b001"
	options.SessionID = "0195f0f0-0000-4000-8000-00000000b002"
	options.Recorder = func(context.Context, contract.ErrorEvent) (database.RecordResult, error) {
		recorded++
		return database.RecordResult{}, nil
	}
	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(result.Stdout, "helper stdout line") {
		t.Fatalf("stdout capture missing its line: %q", result.Stdout)
	}
	if strings.Contains(result.Stdout, "helper stderr line") {
		t.Fatalf("stdout capture contains the stderr line")
	}
	if !strings.Contains(result.Stderr, "helper stderr line") {
		t.Fatalf("stderr capture missing its line: %q", result.Stderr)
	}
	if strings.Contains(result.Stderr, "helper stdout line") {
		t.Fatalf("stderr capture contains the stdout line")
	}
	if recorded != 0 || result.Event != nil {
		t.Fatalf("a successful command produced a record")
	}
}

func TestRunStartFailureIsAnError(t *testing.T) {
	_, err := capture.Run(context.Background(), capture.RunOptions{
		Command: []string{"errkb-command-that-does-not-exist"},
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err == nil {
		t.Fatalf("a missing executable should be an error")
	}
}

func TestRunTruncatesLongOutput(t *testing.T) {
	options := helperOptions("flood")
	options.CaptureLimit = 1024
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.StdoutDropped == 0 {
		t.Fatalf("expected dropped bytes for a 200 KB capture limit 1024")
	}
	if !strings.Contains(result.Stdout, "[errkb:") || !strings.Contains(result.Stdout, "bytes omitted") {
		t.Fatalf("truncation marker missing: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, " error: tail line after the flood") {
		t.Fatalf("tail of the output should survive the cap")
	}
	if len(result.Stdout) > 3*1024 {
		t.Fatalf("retained output is %d bytes, the cap is not holding", len(result.Stdout))
	}
}

func TestRunForwardsStdin(t *testing.T) {
	options := helperOptions("stdin")
	options.Stdin = strings.NewReader("hello from the caller")
	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(result.Stdout, "hello from the caller") {
		t.Fatalf("stdin was not forwarded: %q", result.Stdout)
	}
}

func TestRunStreamsOutputLive(t *testing.T) {
	writer := newSignalWriter()
	options := helperOptions("live")
	options.Stdout = writer
	options.Stderr = io.Discard
	done := make(chan struct{})
	var result capture.RunResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = capture.Run(context.Background(), options)
	}()
	select {
	case <-writer.first:
	case <-done:
		t.Fatalf("output appeared only after the command finished")
	case <-time.After(5 * time.Second):
		t.Fatalf("no live output within 5s")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the wrapper did not return after the command exited")
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !strings.Contains(result.Stdout, "second-line") {
		t.Fatalf("the capture lost the second line: %q", result.Stdout)
	}
}

func TestRunCancellationStopsTheChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	options := helperOptions("sleep")
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	result, err := capture.Run(ctx, options)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("a cancelled child should still return a result: %v", err)
	}
	if elapsed > 6*time.Second {
		t.Fatalf("cancellation took %s; the child was not stopped", elapsed)
	}
	if result.ExitCode == 0 {
		t.Fatalf("a cancelled sleep should not report success")
	}
	<-done
}

func TestRunEnvironmentSummaryIsWhitelisted(t *testing.T) {
	summary := capture.SummarizeEnvironment()
	if summary.OS == "" || summary.Arch == "" || summary.RuntimeVersions["go"] == "" {
		t.Fatalf("exported environment summary is incomplete: %+v", summary)
	}
	options := helperOptions("fail")
	options.ProjectKey = "capture-test"
	options.ConnectorID = "0195f0f0-0000-4000-8000-00000000b001"
	options.SessionID = "0195f0f0-0000-4000-8000-00000000b002"
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	var captured contract.ErrorEvent
	options.Recorder = func(_ context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
		captured = event
		return database.RecordResult{Created: true}, nil
	}
	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Event == nil {
		t.Fatalf("expected a recorded event")
	}
	if captured.OS == "" {
		t.Fatalf("environment summary lost the OS")
	}
	if len(captured.RuntimeVersions) != 1 || captured.RuntimeVersions["go"] == "" {
		t.Fatalf("runtime versions should carry exactly the go entry: %v", captured.RuntimeVersions)
	}
	encoded, err := json.Marshal(captured)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	text := strings.ToLower(string(encoded))
	for _, banned := range []string{"sk-", "bearer", "api_key", "users/", "home/"} {
		if strings.Contains(text, banned) {
			t.Fatalf("environment summary leaked %q: %s", banned, encoded)
		}
	}
}

func TestRunRecordsFailureAndRepeatsLandOnTheSameIssue(t *testing.T) {
	setup := newCaptureDatabase(t)
	options := helperOptions("fail")
	options.ProjectKey = "capture-test"
	options.ConnectorID = setup.connectorID
	options.SessionID = setup.sessionID
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	options.Recorder = setup.record

	first, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Event == nil || first.Recorded == nil {
		t.Fatalf("first failure was not recorded")
	}
	if !first.Recorded.Created {
		t.Fatalf("first failure should create the issue")
	}
	if first.ExitCode != 1 {
		t.Fatalf("exit code changed: %d", first.ExitCode)
	}
	event := first.Event
	if event.Channel != capture.ChannelB {
		t.Fatalf("channel = %q, want %q", event.Channel, capture.ChannelB)
	}
	if event.Category != "command-exit" {
		t.Fatalf("category = %q, want command-exit", event.Category)
	}
	if len(event.Fingerprint) != 12 {
		t.Fatalf("fingerprint = %q, want 12 hex characters", event.Fingerprint)
	}
	if event.Command == "" || event.ExitCode == nil || *event.ExitCode != 1 {
		t.Fatalf("command/exit code missing from the event")
	}
	if first.Classification.Category == "" {
		t.Fatalf("classification missing from the result")
	}

	second, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Event == nil || second.Recorded == nil {
		t.Fatalf("second failure was not recorded")
	}
	if second.Recorded.IssueID != first.Recorded.IssueID {
		t.Fatalf("repeat run created a different issue: %s vs %s", second.Recorded.IssueID, first.Recorded.IssueID)
	}
	if second.Recorded.EventID == first.Recorded.EventID {
		t.Fatalf("each run is a new event; the event id must differ")
	}
	if second.Event.Fingerprint != first.Event.Fingerprint {
		t.Fatalf("fingerprint moved between identical runs: %s vs %s", second.Event.Fingerprint, first.Event.Fingerprint)
	}

	var events, occurrences, issues, hits int
	ctx := context.Background()
	if err := setup.conn.QueryRow(ctx, `select count(*) from events`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := setup.conn.QueryRow(ctx, `select count(*) from occurrences`).Scan(&occurrences); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	if err := setup.conn.QueryRow(ctx, `select count(*) from issues`).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := setup.conn.QueryRow(ctx, `select hits from issues`).Scan(&hits); err != nil {
		t.Fatalf("read hits: %v", err)
	}
	if events != 2 || occurrences != 2 || issues != 1 || hits != 2 {
		t.Fatalf("events=%d occurrences=%d issues=%d hits=%d, want 2/2/1/2", events, occurrences, issues, hits)
	}
}

func TestRunSkipsTransientFailures(t *testing.T) {
	options := helperOptions("transient")
	options.ProjectKey = "capture-test"
	options.ConnectorID = "0195f0f0-0000-4000-8000-00000000a001"
	options.SessionID = "0195f0f0-0000-4000-8000-00000000a002"
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	recorded := 0
	options.Recorder = func(context.Context, contract.ErrorEvent) (database.RecordResult, error) {
		recorded++
		return database.RecordResult{}, nil
	}
	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Transient {
		t.Fatalf("an i/o timeout should be classified transient")
	}
	if result.Event != nil || recorded != 0 {
		t.Fatalf("transient failure reached the permanent record path")
	}
}

func TestRunRedactsSecretsBeforeRecording(t *testing.T) {
	setup := newCaptureDatabase(t)
	options := helperOptions("secret")
	options.ProjectKey = "capture-test"
	options.ConnectorID = setup.connectorID
	options.SessionID = setup.sessionID
	options.Stdout = io.Discard
	options.Stderr = io.Discard
	options.Recorder = setup.record

	result, err := capture.Run(context.Background(), options)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Event == nil {
		t.Fatalf("the failure was not recorded")
	}
	if !strings.Contains(result.Event.Headline, "<secret>") {
		t.Fatalf("headline was not redacted: %q", result.Event.Headline)
	}
	if !strings.Contains(result.Event.RedactedSample, "<secret>") {
		t.Fatalf("sample was not redacted: %q", result.Event.RedactedSample)
	}
	stored := readStoredText(t, setup, result.Recorded.EventID)
	if strings.Contains(stored, "sk-") {
		t.Fatalf("stored row leaked the key: %q", stored)
	}
	if !strings.Contains(stored, "<secret>") {
		t.Fatalf("stored row lost the redaction marker: %q", stored)
	}
}

// signalWriter signals on the first byte and buffers everything.
type signalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	once  sync.Once
	first chan struct{}
}

func newSignalWriter() *signalWriter {
	return &signalWriter{first: make(chan struct{})}
}

func (writer *signalWriter) Write(p []byte) (int, error) {
	writer.mu.Lock()
	writer.buf.Write(p)
	writer.mu.Unlock()
	writer.once.Do(func() { close(writer.first) })
	return len(p), nil
}

// captureDatabase is a migrated schema with one connector and one session.
type captureDatabase struct {
	conn        *pgx.Conn
	schema      string
	connectorID string
	sessionID   string
}

func newCaptureDatabase(t *testing.T) *captureDatabase {
	t.Helper()
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the database-backed capture tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("errkb_capture_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	t.Cleanup(func() {
		closeCtx := context.Background()
		_, _ = conn.Exec(closeCtx, "drop schema if exists "+schema+" cascade")
		_ = conn.Close(closeCtx)
	})
	if _, err := database.Migrate(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var connectorID, sessionID string
	if err := conn.QueryRow(ctx,
		`insert into connectors (name, channel, source, actor) values ('capture-test', 'B', 'capture', 'test') returning id`,
	).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`insert into sessions (connector_id, external_id, started_at, source, actor)
		 values ($1, 'capture-session', now(), 'capture', 'test') returning id`,
		connectorID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return &captureDatabase{conn: conn, schema: schema, connectorID: connectorID, sessionID: sessionID}
}

func (setup *captureDatabase) record(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
	return database.RecordEvent(ctx, setup.conn, event)
}

func readStoredText(t *testing.T, setup *captureDatabase, eventID string) string {
	t.Helper()
	ctx := context.Background()
	var headline, normalized, sample string
	if err := setup.conn.QueryRow(ctx,
		`select headline, normalized_message, redacted_sample from events where event_id = $1`, eventID,
	).Scan(&headline, &normalized, &sample); err != nil {
		t.Fatalf("read stored event: %v", err)
	}
	return headline + "\n" + normalized + "\n" + sample
}
