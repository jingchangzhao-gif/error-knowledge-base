//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// fakeClient stands in for the daemon; the CLI only ever sees this interface.
type fakeClient struct {
	mu       sync.Mutex
	ops      []string
	err      error
	lookup   *contract.LookupResponse
	search   *contract.SearchResponse
	record   *contract.RecordResponse
	feedback *contract.FeedbackResponse
	list     *contract.ListResponse
	stats    *contract.StatsResponse
}

func (f *fakeClient) note(operation string) {
	f.mu.Lock()
	f.ops = append(f.ops, operation)
	f.mu.Unlock()
}

func (f *fakeClient) Lookup(context.Context, contract.LookupRequest) (contract.LookupResponse, error) {
	f.note("lookup")
	if f.err != nil {
		return contract.LookupResponse{}, f.err
	}
	if f.lookup != nil {
		return *f.lookup, nil
	}
	return contract.LookupResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{}}, nil
}

func (f *fakeClient) Search(context.Context, contract.SearchRequest) (contract.SearchResponse, error) {
	f.note("search")
	if f.err != nil {
		return contract.SearchResponse{}, f.err
	}
	if f.search != nil {
		return *f.search, nil
	}
	return contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{}}, nil
}

func (f *fakeClient) GetIssue(_ context.Context, request contract.GetIssueRequest) (contract.GetIssueResponse, error) {
	f.note("get_issue")
	if f.err != nil {
		return contract.GetIssueResponse{}, f.err
	}
	return contract.GetIssueResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Issue: contract.IssueDetailV{
			ID:          request.IssueID,
			Fingerprint: "000000000000",
			Title:       "fake issue",
		},
	}, nil
}

func (f *fakeClient) SetSolutionStatus(_ context.Context, request contract.SetSolutionStatusRequest) (contract.SetSolutionStatusResponse, error) {
	f.note("set_solution_status")
	if f.err != nil {
		return contract.SetSolutionStatusResponse{}, f.err
	}
	return contract.SetSolutionStatusResponse{SchemaVersion: contract.ToolSchemaVersion, SolutionID: request.SolutionID, Status: request.Status}, nil
}

func (f *fakeClient) DeleteSolution(_ context.Context, request contract.DeleteSolutionRequest) (contract.DeleteSolutionResponse, error) {
	f.note("delete_solution")
	if f.err != nil {
		return contract.DeleteSolutionResponse{}, f.err
	}
	return contract.DeleteSolutionResponse{SchemaVersion: contract.ToolSchemaVersion, SolutionID: request.SolutionID, Deleted: true}, nil
}
func (f *fakeClient) Record(context.Context, contract.RecordRequest) (contract.RecordResponse, error) {
	f.note("record")
	if f.err != nil {
		return contract.RecordResponse{}, f.err
	}
	if f.record != nil {
		return *f.record, nil
	}
	return contract.RecordResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		EventID:       "11111111-1111-4111-8111-111111111111",
		OccurrenceID:  "22222222-2222-4222-8222-222222222222",
		IssueID:       "00000000-0000-4000-8000-000000000001",
		Fingerprint:   "0123456789ab",
		Created:       true,
	}, nil
}

func (f *fakeClient) Capture(context.Context, contract.CaptureRequest) (contract.CaptureResponse, error) {
	f.note("capture")
	if f.err != nil {
		return contract.CaptureResponse{}, f.err
	}
	return contract.CaptureResponse{
		SchemaVersion: contract.ConnectorSDKVersion,
		EventID:       "11111111-1111-4111-8111-111111111111",
		OccurrenceID:  "22222222-2222-4222-8222-222222222222",
		IssueID:       "00000000-0000-4000-8000-000000000001",
		Fingerprint:   "0123456789ab",
		Created:       true,
	}, nil
}

func (f *fakeClient) Feedback(context.Context, contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	f.note("feedback")
	if f.err != nil {
		return contract.FeedbackResponse{}, f.err
	}
	if f.feedback != nil {
		return *f.feedback, nil
	}
	return contract.FeedbackResponse{SchemaVersion: contract.ToolSchemaVersion, OutcomeID: "outcome-1", Recorded: true}, nil
}

func (f *fakeClient) List(context.Context, contract.ListRequest) (contract.ListResponse, error) {
	f.note("list")
	if f.err != nil {
		return contract.ListResponse{}, f.err
	}
	if f.list != nil {
		return *f.list, nil
	}
	return contract.ListResponse{SchemaVersion: contract.ToolSchemaVersion, Items: []contract.ListItem{}}, nil
}

func (f *fakeClient) Stats(context.Context, contract.StatsRequest) (contract.StatsResponse, error) {
	f.note("stats")
	if f.err != nil {
		return contract.StatsResponse{}, f.err
	}
	if f.stats != nil {
		return *f.stats, nil
	}
	return contract.StatsResponse{SchemaVersion: contract.ToolSchemaVersion}, nil
}

func (f *fakeClient) RecordSolution(_ context.Context, request contract.RecordSolutionRequest) (contract.RecordSolutionResponse, error) {
	f.note("record_solution")
	if f.err != nil {
		return contract.RecordSolutionResponse{}, f.err
	}
	return contract.RecordSolutionResponse{
		SchemaVersion:     contract.ToolSchemaVersion,
		SolutionID:        "00000000-0000-4000-8000-000000000001",
		SolutionVersionID: "00000000-0000-4000-8000-000000000002",
		Version:           1,
		Recorded:          true,
	}, nil
}

func (f *fakeClient) SetStatus(_ context.Context, request contract.SetStatusRequest) (contract.SetStatusResponse, error) {
	f.note("set_status")
	if f.err != nil {
		return contract.SetStatusResponse{}, f.err
	}
	return contract.SetStatusResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		IssueID:       request.IssueID,
		Status:        request.Status,
	}, nil
}

func (f *fakeClient) PreventionAdd(context.Context, contract.PreventionAddRequest) (contract.PreventionAddResponse, error) {
	f.note(contract.OperationPreventionAdd)
	if f.err != nil {
		return contract.PreventionAddResponse{}, f.err
	}
	return contract.PreventionAddResponse{SchemaVersion: "v1", Artifact: preventionCLIArtifact(), Version: preventionCLIVersion("draft"), Created: true}, nil
}

func (f *fakeClient) PreventionVerify(context.Context, contract.PreventionVerifyRequest) (contract.PreventionVerifyResponse, error) {
	f.note(contract.OperationPreventionVerify)
	if f.err != nil {
		return contract.PreventionVerifyResponse{}, f.err
	}
	return contract.PreventionVerifyResponse{SchemaVersion: "v1", Verification: preventionCLIVerification(true), Version: preventionCLIVersion("verified")}, nil
}

func (f *fakeClient) PreventionActivate(context.Context, contract.PreventionActivateRequest) (contract.PreventionActivateResponse, error) {
	f.note(contract.OperationPreventionActivate)
	if f.err != nil {
		return contract.PreventionActivateResponse{}, f.err
	}
	return contract.PreventionActivateResponse{SchemaVersion: "v1", Version: preventionCLIVersion("active"), Activated: true}, nil
}

func (f *fakeClient) PreventionList(context.Context, contract.PreventionListRequest) (contract.PreventionListResponse, error) {
	f.note(contract.OperationPreventionList)
	if f.err != nil {
		return contract.PreventionListResponse{}, f.err
	}
	return contract.PreventionListResponse{SchemaVersion: "v1", Items: []contract.PreventionArtifactView{}}, nil
}

func (f *fakeClient) PreventionGet(context.Context, contract.PreventionGetRequest) (contract.PreventionGetResponse, error) {
	f.note(contract.OperationPreventionGet)
	if f.err != nil {
		return contract.PreventionGetResponse{}, f.err
	}
	return contract.PreventionGetResponse{SchemaVersion: "v1", Artifact: preventionCLIArtifact(), Versions: []contract.PreventionVersion{}, Verifications: []contract.PreventionVerification{}}, nil
}

func (f *fakeClient) Close() error { return nil }

func testEnv(fake *fakeClient) (*cliEnv, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return &cliEnv{
		stdout: stdout,
		stderr: stderr,
		stdin:  strings.NewReader(""),
		dial: func(context.Context) (apiClient, error) {
			return fake, nil
		},
	}, stdout, stderr
}

func failingDialEnv(dialErr error) (*cliEnv, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return &cliEnv{
		stdout: stdout,
		stderr: stderr,
		stdin:  strings.NewReader(""),
		dial: func(context.Context) (apiClient, error) {
			return nil, dialErr
		},
	}, stdout, stderr
}

func candidateFixture(approximate bool) contract.Candidate {
	confidence := 0.9
	reason := "exact_fingerprint"
	detail := "same normalized message"
	if approximate {
		confidence = 0.72
		reason = "jaccard_rerank"
		detail = "environment differs: os linux -> windows"
	}
	return contract.Candidate{
		IssueID:     "00000000-0000-4000-8000-000000000001",
		Fingerprint: "0123456789ab",
		Title:       "EPERM when renaming the package",
		MatchReason: reason,
		MatchDetail: detail,
		Approximate: approximate,
		Confidence:  &confidence,
	}
}

func eventFixture(t *testing.T) []byte {
	t.Helper()
	transient := false
	confidence := 0.9
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           "11111111-1111-4111-8111-111111111111",
		ConnectorID:       "connector-1",
		SessionID:         "session-1",
		ObservedAt:        "2026-10-19T00:00:00Z",
		Channel:           "E",
		Category:          "tool",
		Severity:          "error",
		Transient:         &transient,
		Headline:          "EPERM when renaming the package",
		NormalizedMessage: "eperm: operation not permitted, rename '<path>/pkg'",
		RedactedSample:    "eperm: operation not permitted, rename '<path>/pkg'",
		OS:                "windows",
		ProjectKey:        "demo",
		Fingerprint:       "0123456789ab",
		Evidence:          "manual",
		Confidence:        &confidence,
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal the event fixture: %v", err)
	}
	return encoded
}

func TestJSONOutputMatchesTheLocalAPIContract(t *testing.T) {
	validList := &contract.ListResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Items: []contract.ListItem{{
			ID:        "00000000-0000-4000-8000-000000000001",
			Title:     "EPERM when renaming the package",
			UpdatedAt: "2026-10-19T00:00:00Z",
		}},
		NextCursor: "next-1",
	}
	validStats := &contract.StatsResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Totals:        contract.StatsTotals{Events: 7, Issues: 3, Solutions: 2, Outcomes: 1},
		Retrieval:     contract.StatsRetrieval{P50MS: 2, P95MS: 9},
		Tokens:        contract.StatsTokens{EstimatedSaved: 120, MeasuredSaved: 40, MeasuredEvents: 2},
	}
	cases := []struct {
		name    string
		command string
		args    []string
		prepare func(*fakeClient)
		check   func([]byte) error
	}{
		{
			name:    "search",
			command: "search",
			args:    []string{"eperm rename"},
			prepare: func(fake *fakeClient) {
				fake.search = &contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{candidateFixture(false)}}
			},
			check: func(payload []byte) error {
				var response contract.SearchResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			name:    "show",
			command: "show",
			args:    []string{"00000000-0000-4000-8000-000000000001"},
			prepare: func(fake *fakeClient) {
				fake.lookup = &contract.LookupResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{candidateFixture(false)}}
			},
			check: func(payload []byte) error {
				var response contract.LookupResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			name:    "record",
			command: "record",
			args:    nil,
			prepare: func(fake *fakeClient) {},
			check: func(payload []byte) error {
				var response contract.RecordResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			name:    "list",
			command: "list",
			args:    []string{"--kind", "issue"},
			prepare: func(fake *fakeClient) { fake.list = validList },
			check: func(payload []byte) error {
				var response contract.ListResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			name:    "feedback",
			command: "feedback",
			args:    []string{"00000000-0000-4000-8000-000000000001", "33333333-3333-4333-8333-333333333333", "--outcome", "success"},
			prepare: func(fake *fakeClient) {},
			check: func(payload []byte) error {
				var response contract.FeedbackResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
		{
			name:    "stats",
			command: "stats",
			args:    nil,
			prepare: func(fake *fakeClient) { fake.stats = validStats },
			check: func(payload []byte) error {
				var response contract.StatsResponse
				if err := json.Unmarshal(payload, &response); err != nil {
					return err
				}
				return response.Validate()
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &fakeClient{}
			testCase.prepare(fake)
			env, stdout, stderr := testEnv(fake)
			env.json = true
			if testCase.name == "record" {
				env.stdin = bytes.NewReader(eventFixture(t))
			}
			if code := runCommand(testCase.command, testCase.args, env); code != exitOK {
				t.Fatalf("runCommand(%s) = %d, stderr = %q", testCase.command, code, stderr.String())
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("--json output is not valid JSON: %q", stdout.String())
			}
			if err := testCase.check(stdout.Bytes()); err != nil {
				t.Fatalf("--json output does not satisfy the contract: %v", err)
			}
		})
	}
}

func TestHumanOutputExplainsTheMatch(t *testing.T) {
	fake := &fakeClient{search: &contract.SearchResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Candidates:    []contract.Candidate{candidateFixture(true)},
	}}
	env, stdout, stderr := testEnv(fake)
	if code := runCommand("search", []string{"eperm rename"}, env); code != exitOK {
		t.Fatalf("search = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"EPERM when renaming the package",
		"reason jaccard_rerank",
		"APPROXIMATE - confirm before reuse",
		"environment differs: os linux -> windows",
		"confidence 0.72",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("human output is missing %q:\n%s", want, output)
		}
	}
}

func TestExactMatchHumanOutputIsNotApproximate(t *testing.T) {
	fake := &fakeClient{search: &contract.SearchResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Candidates:    []contract.Candidate{candidateFixture(false)},
	}}
	env, stdout, stderr := testEnv(fake)
	if code := runCommand("search", []string{"boom"}, env); code != exitOK {
		t.Fatalf("search = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, "APPROXIMATE") {
		t.Errorf("an exact match must not carry the approximate marker:\n%s", output)
	}
	if !strings.Contains(output, "reason exact_fingerprint") {
		t.Errorf("the match reason is missing:\n%s", output)
	}
}

func TestExitCodesAreSemantic(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		fake := &fakeClient{search: &contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: []contract.Candidate{candidateFixture(false)}}}
		env, _, _ := testEnv(fake)
		if code := runCommand("search", []string{"boom"}, env); code != exitOK {
			t.Fatalf("exit code = %d, want %d", code, exitOK)
		}
	})
	t.Run("not found", func(t *testing.T) {
		env, stdout, _ := testEnv(&fakeClient{})
		if code := runCommand("search", []string{"boom"}, env); code != exitNotFound {
			t.Fatalf("exit code = %d, want %d", code, exitNotFound)
		}
		if !strings.Contains(stdout.String(), "no matching issue yet") {
			t.Fatalf("stdout = %q, want the empty-result explanation", stdout.String())
		}
	})
	t.Run("bad arguments", func(t *testing.T) {
		env, _, _ := testEnv(&fakeClient{})
		if code := runCommand("search", nil, env); code != exitUsage {
			t.Fatalf("missing query exit code = %d, want %d", code, exitUsage)
		}
		env, _, _ = testEnv(&fakeClient{})
		if code := runCommand("search", []string{"--limit", "9", "boom"}, env); code != exitUsage {
			t.Fatalf("invalid limit exit code = %d, want %d", code, exitUsage)
		}
	})
	t.Run("service down", func(t *testing.T) {
		env, stdout, stderr := failingDialEnv(service.ErrServiceUnavailable)
		if code := runCommand("search", []string{"boom"}, env); code != exitUnavailable {
			t.Fatalf("exit code = %d, want %d", code, exitUnavailable)
		}
		message := stderr.String()
		if !strings.Contains(message, "the errkb daemon is not running") && !strings.Contains(message, "cannot reach the daemon") {
			t.Fatalf("stderr = %q, want a readable service-down message", message)
		}
		if strings.Contains(message, "goroutine") || strings.Contains(message, "panic") {
			t.Fatalf("stderr leaks a stack trace: %q", message)
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want nothing when the daemon is down", stdout.String())
		}
	})
	t.Run("business errors", func(t *testing.T) {
		cases := []struct {
			code string
			want int
		}{
			{service.CodeInvalidRequest, exitUsage},
			{service.CodeNotFound, exitNotFound},
			{service.CodeUnavailable, exitUnavailable},
			{service.CodeConflict, exitInternal},
			{service.CodeInternal, exitInternal},
		}
		for _, testCase := range cases {
			fake := &fakeClient{err: &service.CallError{Code: testCase.code, Message: "daemon said no"}}
			env, _, _ := testEnv(fake)
			if code := runCommand("search", []string{"boom"}, env); code != testCase.want {
				t.Errorf("CallError %q exit code = %d, want %d", testCase.code, code, testCase.want)
			}
		}
	})
}

func TestFeedbackHasNoTrustInput(t *testing.T) {
	env, _, stderr := testEnv(&fakeClient{})
	args := []string{"--trust", "1", "00000000-0000-4000-8000-000000000001", "33333333-3333-4333-8333-333333333333", "--outcome", "success"}
	if code := runCommand("feedback", args, env); code != exitUsage {
		t.Fatalf("feedback --trust exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want the unknown-flag rejection", stderr.String())
	}

	env, stdout, _ := testEnv(&fakeClient{})
	args = []string{"00000000-0000-4000-8000-000000000001", "33333333-3333-4333-8333-333333333333", "--outcome", "success"}
	if code := runCommand("feedback", args, env); code != exitOK {
		t.Fatalf("valid feedback exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "recorded outcome") {
		t.Fatalf("stdout = %q, want the confirmation", stdout.String())
	}
}

func TestRecordReadsTheEventFromStdin(t *testing.T) {
	fake := &fakeClient{}
	env, stdout, stderr := testEnv(fake)
	env.stdin = bytes.NewReader(eventFixture(t))
	if code := runCommand("record", nil, env); code != exitOK {
		t.Fatalf("record exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "recorded event") {
		t.Fatalf("stdout = %q, want the confirmation", stdout.String())
	}
}

func TestRecordShowsQueuedEvents(t *testing.T) {
	queued := &contract.RecordResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		EventID:       "11111111-1111-4111-8111-111111111111",
		Fingerprint:   "0123456789ab",
		Queued:        true,
	}
	fake := &fakeClient{record: queued}
	env, stdout, stderr := testEnv(fake)
	env.stdin = bytes.NewReader(eventFixture(t))
	if code := runCommand("record", nil, env); code != exitOK {
		t.Fatalf("record exit code = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "queued for replay (fingerprint 0123456789ab)") {
		t.Fatalf("stdout = %q, want the queued note", output)
	}
	if !strings.Contains(output, "the database is unreachable") {
		t.Fatalf("stdout = %q, want the replay explanation", output)
	}
	if strings.Contains(output, "occurrence") {
		t.Fatalf("stdout = %q, a queued event has no occurrence yet", output)
	}

	jsonEnv, jsonOut, jsonErr := testEnv(fake)
	jsonEnv.json = true
	jsonEnv.stdin = bytes.NewReader(eventFixture(t))
	if code := runCommand("record", nil, jsonEnv); code != exitOK {
		t.Fatalf("record --json exit code = %d, stderr = %q", code, jsonErr.String())
	}
	var response contract.RecordResponse
	if err := json.Unmarshal(jsonOut.Bytes(), &response); err != nil {
		t.Fatalf("record --json output: %v (%q)", err, jsonOut.String())
	}
	if !response.Queued || response.OccurrenceID != "" {
		t.Fatalf("record --json response = %+v, want queued with no occurrence", response)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("record --json response does not satisfy the contract: %v", err)
	}
}

func TestConcurrentCommandsDoNotInterfere(t *testing.T) {
	fake := &fakeClient{stats: &contract.StatsResponse{SchemaVersion: contract.ToolSchemaVersion}}
	var wait sync.WaitGroup
	outputs := make([]string, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			env, stdout, stderr := testEnv(fake)
			if code := runCommand("stats", nil, env); code != exitOK {
				t.Errorf("stats %d exit code = %d, stderr = %q", index, code, stderr.String())
				return
			}
			outputs[index] = stdout.String()
		}(index)
	}
	wait.Wait()
	for index, output := range outputs {
		if !strings.Contains(output, "totals:") {
			t.Errorf("command %d stdout = %q, want its own totals line", index, output)
		}
	}
}

func TestRunStripsTheGlobalJSONSwitch(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	fake := &fakeClient{}
	originalDial := dialClient
	dialClient = func(context.Context) (apiClient, error) { return fake, nil }
	t.Cleanup(func() { dialClient = originalDial })
	if code := run([]string{"--json", "stats"}, stdout, stderr); code != exitOK {
		t.Fatalf("run(--json stats) = %d, stderr = %q", code, stderr.String())
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("the global --json switch did not reach the command: %q", stdout.String())
	}
}
