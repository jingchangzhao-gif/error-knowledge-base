package capture

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

// DefaultCaptureLimit bounds how many bytes of each stream are kept for the
// event and for the caller. The cap keeps a chatty command from exhausting
// memory; the rule below says exactly what survives.
const DefaultCaptureLimit = 64 << 10

// ChannelB is the §3.1 capture level of a CLI wrapper: the wrapper sees the
// process directly, so the output is as reliable as the host allows.
const ChannelB = "B"

// commandCategory is the ErrorEvent category of a wrapped command. The
// contract's category enum describes the capture source (§4, shared with the
// DSH adapter: tool, command-exit, llm, agent); the §1 failure domain that
// core.Classify returns is a separate axis and stays in RunResult.
const commandCategory = "command-exit"

// RunOptions describes one wrapped command.
type RunOptions struct {
	// Command is the argument vector, without the `errkb run --` prefix.
	Command []string
	// Dir is the working directory, empty meaning the current one.
	Dir string
	// Env is the child environment, nil meaning os.Environ().
	Env []string
	// Stdin is handed to the child, nil meaning os.Stdin.
	Stdin io.Reader
	// Stdout and Stderr receive the live stream as the child writes it.
	// nil means os.Stdout and os.Stderr.
	Stdout io.Writer
	Stderr io.Writer
	// CaptureLimit bounds the retained bytes of each stream (head and tail
	// together). Zero selects DefaultCaptureLimit.
	CaptureLimit int
	// ProjectKey is stored on the event and scopes the issue. It is required
	// when Recorder is set.
	ProjectKey string
	// ConnectorID and SessionID identify the persisted connector and session
	// rows the event belongs to. Both are required when Recorder is set.
	ConnectorID string
	SessionID   string
	// Recorder persists the event. Nil runs the command and captures output
	// without storing anything.
	Recorder func(context.Context, contract.ErrorEvent) (database.RecordResult, error)
}

// RunResult is what the caller needs after the child exits.
type RunResult struct {
	ExitCode       int
	Stdout         string
	Stderr         string
	StdoutDropped  int
	StderrDropped  int
	Classification core.Classification
	// Transient failures are routed away from the permanent record path; the
	// queue and its promotion rule belong to EKB-033.
	Transient bool
	// Event is the event that was handed to the recorder, nil when nothing was
	// recorded (success, transient failure, or no recorder configured).
	Event *contract.ErrorEvent
	// Recorded is the persisted identity, nil when nothing was recorded.
	Recorded *database.RecordResult
	// RecordError keeps a storage failure from masking the child's exit code.
	RecordError string
}

// Run executes the wrapped command, mirrors both streams live, and records a
// failure when the command exits non-zero and the failure is not transient.
// The child's exit code is returned unchanged; an error is returned only when
// the command cannot be started or a stream cannot be wired.
func Run(ctx context.Context, options RunOptions) (RunResult, error) {
	if len(options.Command) == 0 {
		return RunResult{}, errors.New("run: a command is required")
	}
	limit := options.CaptureLimit
	if limit <= 0 {
		limit = DefaultCaptureLimit
	}
	if options.Recorder != nil {
		if options.ProjectKey == "" {
			return RunResult{}, errors.New("run: a project key is required when recording")
		}
		if options.ConnectorID == "" || options.SessionID == "" {
			return RunResult{}, errors.New("run: a connector and a session are required when recording")
		}
	}
	liveOut := options.Stdout
	if liveOut == nil {
		liveOut = os.Stdout
	}
	liveErr := options.Stderr
	if liveErr == nil {
		liveErr = os.Stderr
	}
	stdin := options.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := newStreamCapture(limit)
	stderr := newStreamCapture(limit)

	cmd := exec.CommandContext(ctx, options.Command[0], options.Command[1:]...)
	cmd.Dir = options.Dir
	cmd.Env = options.Env
	cmd.Stdin = stdin
	// MultiWriter keeps the live stream while capturing; the child is never
	// drained into a buffer first, so interactive programs keep working.
	cmd.Stdout = io.MultiWriter(liveOut, stdout)
	cmd.Stderr = io.MultiWriter(liveErr, stderr)

	runErr := cmd.Run()
	result := RunResult{
		Stdout:        stdout.Text(),
		Stderr:        stderr.Text(),
		StdoutDropped: stdout.Dropped(),
		StderrDropped: stderr.Dropped(),
	}
	exitCode, err := exitStatus(runErr)
	if err != nil {
		return result, err
	}
	result.ExitCode = exitCode
	if exitCode == 0 {
		return result, nil
	}

	summary := SummarizeEnvironment()
	text := stderr.AnalysisText()
	if strings.TrimSpace(text) == "" {
		text = stdout.AnalysisText()
	}
	event, classification := buildFailureEvent(options, text, exitCode, summary)
	result.Classification = classification
	result.Transient = classification.Transient
	if classification.Transient {
		// Transient noise is not a permanent issue on its own. The retry
		// counter and promotion rule live in the capture queue (EKB-033).
		return result, nil
	}
	if options.Recorder == nil {
		return result, nil
	}
	recorded, recordErr := options.Recorder(ctx, event)
	if recordErr != nil {
		result.RecordError = recordErr.Error()
		return result, nil
	}
	result.Event = &event
	result.Recorded = &recorded
	return result, nil
}

// buildFailureEvent turns captured output into a validated ErrorEvent. The
// message pipeline is the same one the DSH adapter uses: headline, normalize,
// signature, redact. Recording never stores unredacted text.
func buildFailureEvent(options RunOptions, captured string, exitCode int, summary EnvironmentSummary) (contract.ErrorEvent, core.Classification) {
	if strings.TrimSpace(captured) == "" {
		captured = fmt.Sprintf("errkb run: command exited with status %d without output", exitCode)
	}
	headline := core.ExtractHeadline(captured)
	commandLine := strings.Join(options.Command, " ")
	classification := core.Classify(core.ClassifyInput{
		Source:   "command",
		Code:     headline.Code,
		Message:  headline.Line,
		Command:  commandLine,
		ExitCode: &exitCode,
	})
	line := redact.Text(headline.Line, redact.Public)
	normalized := core.Normalize(line)
	fingerprint := core.Signature(commandCategory, normalized)
	transient := classification.Transient
	severity := classification.Severity
	confidence := 0.5
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           newEventID(),
		ConnectorID:       options.ConnectorID,
		SessionID:         options.SessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           ChannelB,
		Category:          commandCategory,
		Severity:          string(severity),
		Transient:         &transient,
		Headline:          line,
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(captured, redact.Public),
		ErrorCode:         headline.Code,
		Command:           commandLine,
		ExitCode:          &exitCode,
		ToolName:          filepath.Base(options.Command[0]),
		OS:                summary.OS,
		Shell:             summary.Shell,
		RuntimeVersions:   summary.RuntimeVersions,
		ProjectKey:        options.ProjectKey,
		Fingerprint:       fingerprint,
		Evidence:          "errkb run -- " + commandLine,
		Confidence:        &confidence,
	}
	if err := event.Validate(); err != nil {
		// A validation failure is a programming error in the mapping above, but
		// it must never break the wrapped command: the caller still sees the
		// exit code and the captured streams.
		event = contract.ErrorEvent{}
		return event, classification
	}
	return event, classification
}

// EnvironmentSummary is the whitelist the wrapper records. It is deliberately
// small: no environment dumps, no personal directories, no secrets.
type EnvironmentSummary struct {
	OS              string
	Arch            string
	Shell           string
	RuntimeVersions map[string]string
}

// SummarizeEnvironment reads only the whitelisted values and keeps them short.
func SummarizeEnvironment() EnvironmentSummary {
	summary := EnvironmentSummary{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		RuntimeVersions: map[string]string{
			"go": runtime.Version(),
		},
	}
	for _, key := range []string{"COMSPEC", "SHELL"} {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		// Only the executable name: a shell path can carry a user name.
		summary.Shell = filepath.Base(value)
		break
	}
	if summary.Shell != "" {
		summary.Shell = redact.Text(summary.Shell, redact.Public)
	}
	return summary
}

// summarizeEnvironment keeps the existing internal capture call sites aligned
// while prevention uses the exported whitelist directly.
func summarizeEnvironment() EnvironmentSummary { return SummarizeEnvironment() }

// streamCapture keeps the first and the last half of the allowed bytes, so a
// headline near either edge survives. The middle is replaced by one marker
// line that carries the number of omitted bytes.
type streamCapture struct {
	headLimit int
	tailLimit int
	head      []byte
	tail      []byte
	dropped   int
}

func newStreamCapture(limit int) *streamCapture {
	if limit <= 0 {
		limit = DefaultCaptureLimit
	}
	head := limit / 2
	return &streamCapture{
		headLimit: head,
		tailLimit: limit - head,
		head:      make([]byte, 0, head),
		tail:      make([]byte, 0, limit-head),
	}
}

// Write never fails and always reports the full length, so the live mirror
// keeps receiving every byte even after the capture is full.
func (stream *streamCapture) Write(p []byte) (int, error) {
	written := len(p)
	if room := stream.headLimit - len(stream.head); room > 0 {
		take := min(room, len(p))
		stream.head = append(stream.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return written, nil
	}
	stream.tail = append(stream.tail, p...)
	if excess := len(stream.tail) - stream.tailLimit; excess > 0 {
		// Drop from the front while keeping the newest tailLimit bytes. The
		// copy keeps the backing array bounded.
		stream.dropped += excess
		stream.tail = append(stream.tail[:0], stream.tail[excess:]...)
	}
	return written, nil
}

// Dropped reports the bytes that fell out of the middle of the capture.
func (stream *streamCapture) Dropped() int {
	return stream.dropped
}

// Text is the retained output with an explicit truncation marker.
func (stream *streamCapture) Text() string {
	if stream.dropped == 0 {
		return string(stream.head) + string(stream.tail)
	}
	return fmt.Sprintf("%s\n[errkb: %d bytes omitted from the middle of the output]\n%s",
		stream.head, stream.dropped, stream.tail)
}

// AnalysisText is the retained output without the marker, for headline and
// fingerprint extraction: a variable marker number must not move the
// fingerprint.
func (stream *streamCapture) AnalysisText() string {
	if len(stream.head) == 0 {
		return string(stream.tail)
	}
	if len(stream.tail) == 0 {
		return string(stream.head)
	}
	return string(stream.head) + "\n" + string(stream.tail)
}

// exitStatus turns the cmd.Run error into the child status. A start failure
// (for example a missing executable) is returned as an error; everything else
// is the exit code the caller must see unchanged.
func exitStatus(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			// Killed by a signal (or terminated through the context); report
			// the conventional shell code so scripts still see a failure.
			return 1, nil
		}
		return code, nil
	}
	return -1, err
}

// newEventID builds a v4 UUID without pulling in a dependency.
func newEventID() string {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		// crypto/rand failing is unrecoverable for identity; the timestamp is
		// still unique enough to record the failure and never panics.
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano())
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
