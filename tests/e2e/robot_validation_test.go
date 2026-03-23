package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func runRobotValidationCommand(t *testing.T, bv, workDir string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bv, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "BV_TEST_MODE=1", "BV_NO_BROWSER=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command timed out\nstdout=%s\nstderr=%s", stdout.String(), stderr.String())
	}

	return stdout.String(), stderr.String(), err
}

func assertCommandRejected(t *testing.T, bv, workDir string, args []string, wantMessage string) {
	t.Helper()

	stdout, stderr, err := runRobotValidationCommand(t, bv, workDir, args...)
	if err == nil {
		t.Fatalf("expected command to fail\nstdout=%s\nstderr=%s", stdout, stderr)
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout=%s\nstderr=%s", exitErr.ExitCode(), stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout on rejection, got:\n%s", stdout)
	}
	if stderr == "" || !bytes.Contains([]byte(stderr), []byte(wantMessage)) {
		t.Fatalf("stderr missing expected error\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

func assertModifierRejected(t *testing.T, bv string, args []string, wantMessage string) {
	t.Helper()
	assertCommandRejected(t, bv, t.TempDir(), args, wantMessage)
}

func TestModifierFlagRejection_RobotDiff(t *testing.T) {
	bv := buildBvBinary(t)
	assertModifierRejected(t, bv, []string{"--robot-diff"}, "Error: --robot-diff requires --diff-since")
}

func TestModifierFlagRejection_RobotDrift(t *testing.T) {
	bv := buildBvBinary(t)
	assertModifierRejected(t, bv, []string{"--robot-drift"}, "Error: --robot-drift requires --check-drift")
}

func TestModifierFlagRejection_RobotByLabel(t *testing.T) {
	bv := buildBvBinary(t)
	assertModifierRejected(t, bv, []string{"--robot-by-label=foo"}, "Error: --robot-by-label requires --robot-priority")
}

func TestModifierFlagRejection_RobotByAssignee(t *testing.T) {
	bv := buildBvBinary(t)
	assertModifierRejected(t, bv, []string{"--robot-by-assignee=jeff"}, "Error: --robot-by-assignee requires --robot-priority")
}

func TestValidCombo_RobotDiffWithDiffSince(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir, _ := initGitRepo(t)

	stdout, stderr, err := runRobotValidationCommand(t, bv, repoDir, "--robot-diff", "--diff-since", "HEAD~1")
	if err != nil {
		t.Fatalf("expected command to succeed: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr for valid combo, got:\n%s", stderr)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

func TestValueValidation_InvalidGraphFormat(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := createSimpleRepo(t, 2)
	assertCommandRejected(t, bv, repoDir, []string{"--robot-graph", "--graph-format=bogus"}, `Error: invalid --graph-format "bogus" (expected one of json, dot, mermaid)`)
}

func TestValueValidation_InvalidScriptFormat(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := createSimpleRepo(t, 2)
	assertCommandRejected(t, bv, repoDir, []string{"--emit-script", "--script-format=bogus"}, `Error: invalid --script-format "bogus" (expected one of bash, fish, zsh)`)
}

func TestRobotDispatchRejectsMultiplePrimaryCommands(t *testing.T) {
	bv := buildBvBinary(t)
	repoDir := createSimpleRepo(t, 2)
	assertCommandRejected(t, bv, repoDir, []string{"--robot-triage", "--robot-metrics"}, "Error: multiple primary robot commands specified:")
}
