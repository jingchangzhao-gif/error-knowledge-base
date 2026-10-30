package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testIssueID     = "11111111-1111-1111-1111-111111111111"
	testArtifactID  = "22222222-2222-2222-2222-222222222222"
	testVersionID   = "33333333-3333-3333-3333-333333333333"
	testVerifyID    = "44444444-4444-4444-4444-444444444444"
	testContentHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testCommitHash  = "0123456789abcdef0123456789abcdef01234567"
	testTimestamp   = "2026-10-30T08:00:00Z"
)

type preventionValidator interface{ Validate() error }

func validPreventionArtifact() PreventionArtifact {
	return PreventionArtifact{ArtifactID: testArtifactID, ProjectKey: "demo", Kind: "regression_test", Title: "Lock regression", IssueIDs: []string{testIssueID}, CreatedAt: testTimestamp, UpdatedAt: testTimestamp}
}

func validPreventionVersion() PreventionVersion {
	return PreventionVersion{ArtifactVersionID: testVersionID, ArtifactID: testArtifactID, Version: 1, ArtifactPath: "tests/lock_test.go", ContentSHA256: testContentHash, Status: "draft", CreatedAt: testTimestamp}
}

func validPreventionVerification() PreventionVerification {
	exit := 0
	return PreventionVerification{VerificationID: testVerifyID, ArtifactVersionID: testVersionID, Command: []string{"go", "test", "./tests"}, ExitCode: &exit, Passed: true, DurationMS: 12, Evidence: "ok", Environment: map[string]string{"os": "windows"}, ContentSHA256: testContentHash, CreatedAt: testTimestamp}
}

func validPreventionAdd() PreventionAddRequest {
	return PreventionAddRequest{SchemaVersion: PreventionSchemaVersion, IssueID: testIssueID, Kind: "regression_test", Title: "Lock regression", ArtifactPath: "tests/lock_test.go", ContentSHA256: testContentHash}
}

func validPreventionStats() PreventionStatsResponse {
	commit := testCommitHash
	return PreventionStatsResponse{
		SchemaVersion: PreventionSchemaVersion,
		ByStatus:      []PreventionCount{{Name: "draft", Count: 1}}, ByKind: []PreventionCount{{Name: "regression_test", Count: 1}},
		VerificationRuns: 1, VerificationPassed: 1, VerificationPassRate: 1,
		Activations:       []PreventionActivationStats{{IssueID: testIssueID, ArtifactID: testArtifactID, ArtifactVersionID: testVersionID, ActivatedAt: testTimestamp, CommitSHA: commit, Status: "active", FirstSeenToActivationMS: 100, RecurrenceCount: 0, BeforeCount: 1, AfterCount: 0, BeforeDays: 30, AfterDays: 30, ObservationStatus: "mature"}},
		MatureActivations: 1, MatureZeroRecurrence: 1, RecurringVersionIDs: []string{},
	}
}

func TestPreventionContracts(t *testing.T) {
	one, limit, protection := 1, 100, 1.0
	version := validPreventionVersion()
	verification := validPreventionVerification()
	version.LatestVerification = &verification
	valid := []preventionValidator{
		validPreventionAdd(),
		PreventionAddResponse{SchemaVersion: PreventionSchemaVersion, Artifact: validPreventionArtifact(), Version: version, Created: true},
		PreventionVerifyRequest{SchemaVersion: PreventionSchemaVersion, ArtifactVersionID: testVersionID, RepoPath: `C:\repo`, Command: []string{"go", "test", "./tests"}},
		PreventionVerifyResponse{SchemaVersion: PreventionSchemaVersion, Verification: verification, Version: version},
		PreventionActivateRequest{SchemaVersion: PreventionSchemaVersion, ArtifactVersionID: testVersionID, RepoPath: `C:\repo`},
		PreventionActivateResponse{SchemaVersion: PreventionSchemaVersion, Version: version, Activated: true},
		PreventionListRequest{SchemaVersion: PreventionSchemaVersion, IssueID: testIssueID, ProjectKey: "demo", Kind: "intent", Status: "deprecated", Limit: &limit, Cursor: "next"},
		PreventionListResponse{SchemaVersion: PreventionSchemaVersion, Items: []PreventionArtifactView{}},
		PreventionGetRequest{SchemaVersion: PreventionSchemaVersion, ArtifactID: testArtifactID},
		PreventionGetResponse{SchemaVersion: PreventionSchemaVersion, Artifact: validPreventionArtifact(), Versions: []PreventionVersion{}, Verifications: []PreventionVerification{}},
		PreventionStatsRequest{SchemaVersion: PreventionSchemaVersion, ProjectKey: "demo", IssueID: testIssueID},
		PreventionStatsResponse{SchemaVersion: PreventionSchemaVersion, ByStatus: []PreventionCount{}, ByKind: []PreventionCount{}, Activations: []PreventionActivationStats{}, RecurringVersionIDs: []string{}},
		validPreventionStats(),
	}
	for _, value := range valid {
		if err := value.Validate(); err != nil {
			t.Errorf("%T rejected a valid value: %v", value, err)
		}
	}

	for _, kind := range []string{"regression_test", "deterministic_check", "agent_instruction", "intent"} {
		r := validPreventionAdd()
		r.Kind = kind
		if err := r.Validate(); err != nil {
			t.Errorf("kind %q: %v", kind, err)
		}
	}
	for _, status := range []string{"draft", "verified", "active", "deprecated"} {
		v := validPreventionVersion()
		v.Status = status
		if status == "active" {
			commit, activated := testCommitHash, testTimestamp
			v.CommitSHA = &commit
			v.ActivatedAt = &activated
		}
		if err := v.Validate(); err != nil {
			t.Errorf("status %q: %v", status, err)
		}
	}
	for _, status := range []string{"observing", "mature"} {
		s := validPreventionStats()
		s.Activations[0].ObservationStatus = status
		if err := s.Validate(); err != nil {
			t.Errorf("observation status %q: %v", status, err)
		}
	}

	badPaths := []string{"", "/tests/a.go", "C:" + `\tests\a.go`, "C:/tests/a.go", `tests\a.go`, "tests//a.go", "./a.go", "tests/../a.go", ".git/config", "tests/.git/config"}
	for _, path := range badPaths {
		r := validPreventionAdd()
		r.ArtifactPath = path
		if err := r.Validate(); err == nil {
			t.Errorf("path %q was accepted", path)
		}
	}
	badHashes := []string{"", "ABC", strings.Repeat("A", 64), strings.Repeat("0", 63)}
	for _, hash := range badHashes {
		r := validPreventionAdd()
		r.ContentSHA256 = hash
		if err := r.Validate(); err == nil {
			t.Errorf("content hash %q was accepted", hash)
		}
	}

	invalid := []preventionValidator{
		PreventionAddRequest{SchemaVersion: "v2", IssueID: testIssueID, Kind: "intent", Title: "x", ArtifactPath: "intent.md", ContentSHA256: testContentHash},
		PreventionAddRequest{SchemaVersion: PreventionSchemaVersion, IssueID: testIssueID, Kind: "unknown", Title: "x", ArtifactPath: "intent.md", ContentSHA256: testContentHash},
		PreventionAddRequest{SchemaVersion: PreventionSchemaVersion, IssueID: testIssueID, Kind: "intent", Title: "sk-" + strings.Repeat("a", 16), ArtifactPath: "intent.md", ContentSHA256: testContentHash},
		PreventionVerifyRequest{SchemaVersion: PreventionSchemaVersion, ArtifactVersionID: testVersionID, RepoPath: `C:\repo`, Command: []string{}},
		PreventionVerifyRequest{SchemaVersion: PreventionSchemaVersion, ArtifactVersionID: testVersionID, Command: []string{"go"}},
		PreventionVerifyRequest{SchemaVersion: PreventionSchemaVersion, ArtifactVersionID: testVersionID, RepoPath: `C:\repo`, Command: []string{"go", ""}},
		PreventionListRequest{SchemaVersion: PreventionSchemaVersion, Limit: &one, Status: "unknown"},
		PreventionVersion{ArtifactVersionID: testVersionID, ArtifactID: testArtifactID, Version: 1, ArtifactPath: "a.go", ContentSHA256: testContentHash, Status: "unknown", CreatedAt: testTimestamp},
	}
	zero := 0
	invalid = append(invalid, PreventionListRequest{SchemaVersion: PreventionSchemaVersion, Limit: &zero})
	negativeStats := validPreventionStats()
	negativeStats.VerificationRuns = -1
	invalid = append(invalid, negativeStats)
	negativeDuration := validPreventionVerification()
	negativeDuration.DurationMS = -1
	invalid = append(invalid, negativeDuration)
	secretEvidence := validPreventionVerification()
	secretEvidence.Evidence = "sk-" + strings.Repeat("a", 16)
	invalid = append(invalid, secretEvidence)
	badRate := validPreventionStats()
	badRate.VerificationPassRate = 2
	invalid = append(invalid, badRate)
	badProtection := validPreventionStats()
	badProtection.MatureProtectionRate = &protection
	badProtection.MatureProtectionRate = func() *float64 { v := 2.0; return &v }()
	invalid = append(invalid, badProtection)
	for _, value := range invalid {
		if err := value.Validate(); err == nil {
			t.Errorf("%T accepted an invalid value: %+v", value, value)
		}
	}
}

func TestPreventionPipeStrictDecoding(t *testing.T) {
	requests := map[string]string{
		OperationPreventionAdd:      `{"schema_version":"v1","issue_id":"i","kind":"intent","title":"t","artifact_path":"intent.md","content_sha256":"` + testContentHash + `"}`,
		OperationPreventionVerify:   `{"schema_version":"v1","artifact_version_id":"v","repo_path":"C:\\repo","command":["go","test"]}`,
		OperationPreventionActivate: `{"schema_version":"v1","artifact_version_id":"v","repo_path":"C:\\repo"}`,
		OperationPreventionList:     `{"schema_version":"v1"}`,
		OperationPreventionGet:      `{"schema_version":"v1","artifact_id":"a"}`,
		OperationPreventionStats:    `{"schema_version":"v1"}`,
	}
	responses := map[string]string{
		OperationPreventionAdd:      `{"schema_version":"v1","artifact":{"artifact_id":"a","project_key":"p","kind":"intent","title":"t","issue_ids":["i"],"created_at":"` + testTimestamp + `","updated_at":"` + testTimestamp + `"},"version":{"artifact_version_id":"v","artifact_id":"a","version":1,"artifact_path":"intent.md","content_sha256":"` + testContentHash + `","status":"draft","created_at":"` + testTimestamp + `"},"created":true}`,
		OperationPreventionVerify:   `{"schema_version":"v1","verification":{"verification_id":"r","artifact_version_id":"v","command":["go","test"],"exit_code":null,"passed":false,"duration_ms":0,"evidence":"","environment":{},"content_sha256":"` + testContentHash + `","created_at":"` + testTimestamp + `"},"version":{"artifact_version_id":"v","artifact_id":"a","version":1,"artifact_path":"intent.md","content_sha256":"` + testContentHash + `","status":"draft","created_at":"` + testTimestamp + `"}}`,
		OperationPreventionActivate: `{"schema_version":"v1","version":{"artifact_version_id":"v","artifact_id":"a","version":1,"artifact_path":"intent.md","content_sha256":"` + testContentHash + `","status":"draft","created_at":"` + testTimestamp + `"},"activated":false}`,
		OperationPreventionList:     `{"schema_version":"v1","items":[]}`,
		OperationPreventionGet:      `{"schema_version":"v1","artifact":{"artifact_id":"a","project_key":"p","kind":"intent","title":"t","issue_ids":[],"created_at":"` + testTimestamp + `","updated_at":"` + testTimestamp + `"},"versions":[],"verifications":[]}`,
		OperationPreventionStats:    `{"schema_version":"v1","by_status":[],"by_kind":[],"verification_runs":0,"verification_passed":0,"verification_pass_rate":0,"activations":[],"mature_activations":0,"mature_zero_recurrence":0,"mature_protection_rate":null,"recurring_version_ids":[]}`,
	}
	for operation, payload := range requests {
		envelope := `{"schema_version":"v1","request_id":"r","operation":"` + operation + `","payload":` + payload + `}`
		if _, err := DecodePipeRequest([]byte(envelope)); err != nil {
			t.Errorf("request %s: %v", operation, err)
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(payload), &fields); err != nil {
			t.Fatal(err)
		}
		fields["unknown"] = true
		unknown, _ := json.Marshal(fields)
		envelope = `{"schema_version":"v1","request_id":"r","operation":"` + operation + `","payload":` + string(unknown) + `}`
		if _, err := DecodePipeRequest([]byte(envelope)); err == nil {
			t.Errorf("request %s accepted an unknown field", operation)
		}
	}
	for operation, result := range responses {
		envelope := `{"schema_version":"v1","request_id":"r","operation":"` + operation + `","result":` + result + `}`
		if _, err := DecodePipeResponse([]byte(envelope)); err != nil {
			t.Errorf("response %s: %v", operation, err)
		}
	}
	badVersion := strings.Replace(responses[OperationPreventionAdd], `"status":"draft"`, `"status":"invented"`, 1)
	envelope := `{"schema_version":"v1","request_id":"r","operation":"prevention_add","result":` + badVersion + `}`
	if _, err := DecodePipeResponse([]byte(envelope)); err == nil {
		t.Error("response accepted an invalid status")
	}
	missingArtifact := `{"schema_version":"v1","request_id":"r","operation":"prevention_get","result":{"schema_version":"v1","versions":[],"verifications":[]}}`
	if _, err := DecodePipeResponse([]byte(missingArtifact)); err == nil {
		t.Error("response without artifact identity was accepted")
	}
}
