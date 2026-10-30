package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

func TestHashPreventionArtifactReadsSafeRegularFile(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "checks", "regression.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("assert the rename failure stays fixed\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HashPreventionArtifact(repo, "checks/regression.txt")
	if err != nil {
		t.Fatalf("hash prevention artifact: %v", err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(content))
	if got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}
}

func TestHashPreventionArtifactRejectsUnsafeFiles(t *testing.T) {
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "large"), bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Dir(outside), filepath.Join(repo, "link")); err != nil {
			t.Fatal(err)
		}
	} else {
		command := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(repo, "link"), filepath.Dir(outside))
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("create directory junction fixture: %v: %s", err, output)
		}
	}
	for _, path := range []string{"../outside.txt", ".git/config", "directory", "large", "link/outside.txt", "C:/outside.txt", `checks\regression.txt`} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			if _, err := HashPreventionArtifact(repo, path); err == nil {
				t.Fatalf("unsafe artifact path %q was accepted", path)
			}
		})
	}
}

func TestPreventionHelper(t *testing.T) {
	mode := os.Getenv("ERRKB_PREVENTION_HELPER")
	if mode == "" {
		t.Skip("helper mode runs only as a subprocess")
	}
	switch mode {
	case "pass":
		fmt.Fprintln(os.Stdout, "verification passed")
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "assertion failed")
		os.Exit(7)
	case "secret":
		fmt.Fprintln(os.Stderr, "auth failed for sk-abcdefghijklmnopqrstuvwx")
		os.Exit(1)
	case "flood":
		chunk := strings.Repeat("x", 1024)
		for index := 0; index < 100; index++ {
			fmt.Fprint(os.Stdout, chunk)
		}
		fmt.Fprintln(os.Stdout, "tail")
		os.Exit(1)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "modify":
		if err := os.WriteFile(os.Getenv("ERRKB_PREVENTION_ARTIFACT"), []byte("changed"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(90)
		}
		os.Exit(0)
	default:
		os.Exit(91)
	}
}

func preventionHelperCommand(mode string) []string {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return []string{executable, "-test.run=^TestPreventionHelper$", "-test.v", "--", mode}
}

func preventionTestVersion(repo, hash string) database.PreventionVersion {
	return database.PreventionVersion{ArtifactPath: "artifact.txt", ContentSHA256: hash}
}

func preventionHelperEnv(mode, artifact string) []string {
	return append(os.Environ(), "ERRKB_PREVENTION_HELPER="+mode, "ERRKB_PREVENTION_ARTIFACT="+artifact)
}

// preventionHelperCommand uses the test's environment through the service hook
// installed by TestMain-free helper execution below.
func runPreventionHelper(ctx context.Context, repo, mode, artifact, hash string) database.PreventionVerificationInput {
	command := preventionHelperCommand(mode)
	oldMode, hadMode := os.LookupEnv("ERRKB_PREVENTION_HELPER")
	oldArtifact, hadArtifact := os.LookupEnv("ERRKB_PREVENTION_ARTIFACT")
	_ = os.Setenv("ERRKB_PREVENTION_HELPER", mode)
	_ = os.Setenv("ERRKB_PREVENTION_ARTIFACT", artifact)
	defer func() {
		if hadMode {
			_ = os.Setenv("ERRKB_PREVENTION_HELPER", oldMode)
		} else {
			_ = os.Unsetenv("ERRKB_PREVENTION_HELPER")
		}
		if hadArtifact {
			_ = os.Setenv("ERRKB_PREVENTION_ARTIFACT", oldArtifact)
		} else {
			_ = os.Unsetenv("ERRKB_PREVENTION_ARTIFACT")
		}
	}()
	input, err := executePreventionVerification(ctx, repo, preventionTestVersion(repo, hash), command)
	if err != nil {
		panic(err)
	}
	return input
}

func TestExecutePreventionVerificationCapturesFailuresAsResults(t *testing.T) {
	repo := t.TempDir()
	artifact := filepath.Join(repo, "artifact.txt")
	if err := os.WriteFile(artifact, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPreventionArtifact(repo, "artifact.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode     string
		passed   bool
		exitCode *int
		evidence string
	}{
		{mode: "pass", passed: true, exitCode: preventionInt(0), evidence: "verification passed"},
		{mode: "fail", passed: false, exitCode: preventionInt(7), evidence: "assertion failed"},
		{mode: "secret", passed: false, exitCode: preventionInt(1), evidence: "<secret>"},
		{mode: "flood", passed: false, exitCode: preventionInt(1), evidence: "truncated"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			input := runPreventionHelper(context.Background(), repo, test.mode, artifact, hash)
			if input.Passed != test.passed || input.ContentSHA256 != hash {
				t.Fatalf("verification input = %+v", input)
			}
			if (input.ExitCode == nil) != (test.exitCode == nil) || input.ExitCode != nil && *input.ExitCode != *test.exitCode {
				t.Fatalf("exit code = %v, want %v", input.ExitCode, test.exitCode)
			}
			if !strings.Contains(input.Evidence, test.evidence) {
				t.Fatalf("evidence = %q, want substring %q", input.Evidence, test.evidence)
			}
			if strings.Contains(input.Evidence, "sk-") {
				t.Fatalf("evidence leaked a secret: %q", input.Evidence)
			}
			if input.Environment["os"] == "" || input.Environment["go_version"] == "" || input.Environment["PATH"] != "" {
				t.Fatalf("environment is not whitelisted: %v", input.Environment)
			}
		})
	}
}

func preventionInt(value int) *int { return &value }

func TestExecutePreventionVerificationRecordsTimeoutAndStartFailure(t *testing.T) {
	repo := t.TempDir()
	artifact := filepath.Join(repo, "artifact.txt")
	if err := os.WriteFile(artifact, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, _ := HashPreventionArtifact(repo, "artifact.txt")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	timedOut := runPreventionHelper(ctx, repo, "sleep", artifact, hash)
	if timedOut.Passed || !strings.Contains(timedOut.Evidence, "timed out") {
		t.Fatalf("timeout result = %+v", timedOut)
	}
	input, err := executePreventionVerification(context.Background(), repo, preventionTestVersion(repo, hash), []string{"errkb-no-such-command"})
	if err != nil || input.Passed || input.ExitCode != nil || !strings.Contains(input.Evidence, "start") {
		t.Fatalf("start failure = %+v, %v", input, err)
	}
}

func TestExecutePreventionVerificationFailsWhenCommandChangesArtifact(t *testing.T) {
	repo := t.TempDir()
	artifact := filepath.Join(repo, "artifact.txt")
	if err := os.WriteFile(artifact, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, _ := HashPreventionArtifact(repo, "artifact.txt")
	input := runPreventionHelper(context.Background(), repo, "modify", artifact, hash)
	if input.Passed || !strings.Contains(input.Evidence, "changed") {
		t.Fatalf("artifact mutation result = %+v", input)
	}
	if input.ContentSHA256 != hash {
		t.Fatalf("verification stored post-command hash instead of registered hash: %q", input.ContentSHA256)
	}
}

func TestPreventionGitCommitAcceptsOnlyCleanTrackedArtifact(t *testing.T) {
	for _, state := range []string{"clean", "untracked", "staged", "modified"} {
		t.Run(state, func(t *testing.T) {
			repo := initPreventionGitRepo(t, state)
			beforeHead := preventionGitOutput(t, repo, "rev-parse", "HEAD")
			beforeIndex := preventionGitOutput(t, repo, "diff", "--cached")
			beforeWorktree, err := os.ReadFile(filepath.Join(repo, "artifact.txt"))
			if state == "untracked" && err != nil {
				t.Fatal(err)
			}
			commit, err := preventionGitCommit(context.Background(), repo, "artifact.txt")
			if state == "clean" {
				if err != nil || commit != beforeHead || len(commit) != 40 {
					t.Fatalf("clean git commit = %q, %v, want %q", commit, err, beforeHead)
				}
			} else if err == nil {
				t.Fatalf("%s artifact was accepted at %q", state, commit)
			}
			afterHead := preventionGitOutput(t, repo, "rev-parse", "HEAD")
			afterIndex := preventionGitOutput(t, repo, "diff", "--cached")
			afterWorktree, readErr := os.ReadFile(filepath.Join(repo, "artifact.txt"))
			if state == "untracked" && readErr != nil {
				t.Fatal(readErr)
			}
			if beforeHead != afterHead || beforeIndex != afterIndex || !bytes.Equal(beforeWorktree, afterWorktree) {
				t.Fatalf("git activation mutated repository state")
			}
		})
	}
}

func TestPreventionGitCommitRejectsNonRepositoryAndOutsidePath(t *testing.T) {
	if _, err := preventionGitCommit(context.Background(), t.TempDir(), "artifact.txt"); err == nil {
		t.Fatal("non-repository was accepted")
	}
	repo := initPreventionGitRepo(t, "clean")
	if _, err := preventionGitCommit(context.Background(), repo, "../artifact.txt"); err == nil {
		t.Fatal("path outside repository was accepted")
	}
}

func initPreventionGitRepo(t *testing.T, state string) string {
	t.Helper()
	repo := t.TempDir()
	preventionGitOutput(t, repo, "init")
	preventionGitOutput(t, repo, "config", "user.name", "Prevention Test")
	preventionGitOutput(t, repo, "config", "user.email", "prevention@example.invalid")
	artifact := filepath.Join(repo, "artifact.txt")
	if state != "untracked" {
		if err := os.WriteFile(artifact, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		preventionGitOutput(t, repo, "add", "--", "artifact.txt")
	} else if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	} else {
		preventionGitOutput(t, repo, "add", "--", "tracked.txt")
	}
	preventionGitOutput(t, repo, "commit", "-m", "fixture")
	switch state {
	case "untracked":
		if err := os.WriteFile(artifact, []byte("untracked"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "staged":
		if err := os.WriteFile(artifact, []byte("staged"), 0o600); err != nil {
			t.Fatal(err)
		}
		preventionGitOutput(t, repo, "add", "--", "artifact.txt")
	case "modified":
		if err := os.WriteFile(artifact, []byte("modified"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func preventionGitOutput(t *testing.T, repo string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = repo
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	return strings.TrimSpace(string(output))
}

func TestPreventionGitUsesOnlyReadOnlyOperations(t *testing.T) {
	// This source-level guard complements the real repository assertions: the
	// implementation must not acquire a porcelain write capability later.
	source, err := os.ReadFile("prevention.go")
	if err == nil {
		for _, forbidden := range []string{`"checkout"`, `"commit"`, `"push"`, `"config"`} {
			if bytes.Contains(source, []byte(forbidden)) {
				t.Fatalf("prevention service contains forbidden git operation %s", forbidden)
			}
		}
	}
}
