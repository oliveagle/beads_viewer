package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	"github.com/Dicklesworthstone/beads_viewer/pkg/recipe"
	flag "github.com/spf13/pflag"
)

func runCommandWithTimeout(t *testing.T, dir, exe string, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BV_NO_BROWSER=1", "BV_TEST_MODE=1")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("command %v timed out\nstdout:\n%s\nstderr:\n%s", args, stdout.String(), stderr.String())
	}

	return stdout.String(), stderr.String(), err
}

func TestFilterByRepo_CaseInsensitiveAndFlexibleSeparators(t *testing.T) {
	issues := []model.Issue{
		{ID: "api-AUTH-1", SourceRepo: "services/api"},
		{ID: "web:UI-2", SourceRepo: "apps/web"},
		{ID: "lib_UTIL_3", SourceRepo: "libs/util"},
		{ID: "misc-4", SourceRepo: "misc"},
	}

	tests := []struct {
		filter   string
		expected int
	}{
		{"API", 1},      // case-insensitive, matches api-
		{"web", 1},      // flexible with ':' separator
		{"lib", 1},      // flexible with '_' separator
		{"missing", 0},  // no match
		{"misc-", 1},    // exact prefix
		{"services", 1}, // matches SourceRepo when ID lacks prefix
	}

	for _, tt := range tests {
		got := filterByRepo(issues, tt.filter)
		if len(got) != tt.expected {
			t.Errorf("filterByRepo(%q) = %d issues, want %d", tt.filter, len(got), tt.expected)
		}
	}
}

func TestRobotFlagsOutputJSON(t *testing.T) {
	tmpDir := t.TempDir()
	beads := `{"id":"A","title":"Root","status":"open","priority":1,"issue_type":"task"}
{"id":"B","title":"Blocked","status":"blocked","priority":2,"issue_type":"task","dependencies":[{"depends_on_id":"A","type":"blocks"}]}`

	if err := os.WriteFile(filepath.Join(tmpDir, ".beads.jsonl"), []byte(beads), 0644); err != nil {
		t.Fatalf("write beads: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, ".beads"), 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".beads", "beads.jsonl"), []byte(beads), 0644); err != nil {
		t.Fatalf("write beads dir: %v", err)
	}

	// Build a temporary bv binary using the repo module
	bin := filepath.Join(tmpDir, "bv")
	build := exec.Command("go", "build", "-C", repoRoot(t), "-o", bin, "./cmd/bv")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build bv: %v\n%s", err, out)
	}

	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return out
	}

	for _, flag := range [][]string{
		{"--robot-plan"},
		{"--robot-insights"},
		{"--robot-priority"},
		{"--robot-recipes"},
		{"--robot-capabilities"},
		{"--robot-docs", "commands"},
		{"--robot-next"},
		{"--robot-triage"},
		{"--robot-label-health"},
		{"--robot-label-flow"},
		{"--robot-label-attention"},
		{"--robot-capacity"},
	} {
		out := run(flag...)
		if !json.Valid(out) {
			t.Fatalf("%v did not return valid JSON: %s", flag, string(out))
		}
	}
}

func TestCLIFlagCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	writeTestBeadsFixture(t, tmpDir)

	exe := buildTestBinary(t)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(exe, args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(), "BV_NO_BROWSER=1", "BV_TEST_MODE=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	t.Run("double-dash robot flag", func(t *testing.T) {
		out := run("--robot-next", "--format", "json")
		if !json.Valid([]byte(out)) {
			t.Fatalf("expected JSON output for long flags, got %q", out)
		}
	})

	t.Run("single-dash compatibility", func(t *testing.T) {
		out := run("-robot-next", "-format", "json")
		if !json.Valid([]byte(out)) {
			t.Fatalf("expected JSON output for single-dash long flags, got %q", out)
		}
	})

	t.Run("short aliases", func(t *testing.T) {
		out := run("--robot-insights", "-l", "backend", "-f", "json")
		if !json.Valid([]byte(out)) {
			t.Fatalf("expected JSON output for short aliases, got %q", out)
		}
	})

	t.Run("grouped help output", func(t *testing.T) {
		out := run("--help")
		for _, snippet := range []string{
			"General Flags:",
			"Search & Filters:",
			"Robot & Planning Flags:",
			"Export & Reporting:",
			"Agent File Management:",
			"--robot-capabilities",
			"-f, --format",
			"-l, --label",
			"-r, --recipe",
		} {
			if !strings.Contains(out, snippet) {
				t.Fatalf("help output missing %q:\n%s", snippet, out)
			}
		}
	})

	t.Run("version flag", func(t *testing.T) {
		out := strings.TrimSpace(run("--version"))
		if !strings.HasPrefix(out, "bv ") {
			t.Fatalf("expected version output, got %q", out)
		}
	})
}

func TestUnknownFlagErrorSuggestsNearestFlag(t *testing.T) {
	exe := buildTestBinary(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "robot flag typo",
			args: []string{"--robot-triag", "--json"},
			want: []string{
				"unknown flag: --robot-triag",
				"Did you mean `bv --robot-triage --json`?",
				"bv --robot-help",
			},
		},
		{
			name: "value flag typo preserves and quotes value",
			args: []string{"--robot-graph", "--graph-rooot=A>B"},
			want: []string{
				"unknown flag: --graph-rooot",
				"Did you mean `bv --robot-graph '--graph-root=A>B'`?",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCommandWithTimeout(t, t.TempDir(), exe, tt.args...)
			if err == nil {
				t.Fatalf("expected unknown flag to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("expected empty stdout for unknown flag, got:\n%s", stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q\nstderr:\n%s", want, stderr)
				}
			}
			if strings.Count(stderr, "unknown flag: --") != 1 {
				t.Fatalf("expected unknown flag error once, got:\n%s", stderr)
			}
		})
	}
}

func TestUnknownCommandErrorSuggestsNearestCommand(t *testing.T) {
	exe := buildTestBinary(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "canonical robot command typo",
			args: []string{"robot-triag", "--json"},
			want: []string{
				`unknown command "robot-triag" for "bv"`,
				"Did you mean `bv robot-triage --json`?",
				"Canonical flag form: `bv --robot-triage --format json`.",
				"bv robot-capabilities --json",
			},
		},
		{
			name: "canonical value command typo preserves args",
			args: []string{"robot-relatd", "A", "--json"},
			want: []string{
				`unknown command "robot-relatd" for "bv"`,
				"Did you mean `bv robot-related A --json`?",
				"Canonical flag form: `bv --robot-related A --format json`.",
			},
		},
		{
			name: "canonical value command typo quotes shell metacharacters",
			args: []string{"robot-relatd", "A>B", "--json"},
			want: []string{
				`unknown command "robot-relatd" for "bv"`,
				"Did you mean `bv robot-related 'A>B' --json`?",
				"Canonical flag form: `bv --robot-related 'A>B' --format json`.",
			},
		},
		{
			name: "agent alias typo preserves args",
			args: []string{"schem", "triage", "--json"},
			want: []string{
				`unknown command "schem" for "bv"`,
				"Did you mean `bv schema triage --json`?",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCommandWithTimeout(t, t.TempDir(), exe, tt.args...)
			if err == nil {
				t.Fatalf("expected unknown command to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("expected empty stdout for unknown command, got:\n%s", stdout)
			}
			for _, want := range tt.want {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q\nstderr:\n%s", want, stderr)
				}
			}
			if strings.Count(stderr, `unknown command "`) != 1 {
				t.Fatalf("expected unknown command error once, got:\n%s", stderr)
			}
		})
	}
}

func TestMissingFlagArgumentErrorSuggestsValueShape(t *testing.T) {
	exe := buildTestBinary(t)

	stdout, stderr, err := runCommandWithTimeout(t, t.TempDir(), exe, "--name")
	if err == nil {
		t.Fatalf("expected missing flag argument to fail\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout for missing flag argument, got:\n%s", stdout)
	}
	for _, want := range []string{"flag needs an argument: --label", "Use --label VALUE."} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q\nstderr:\n%s", want, stderr)
		}
	}
}

func TestRobotNowHonorsSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1234567890")
	requireString(t, robotNow().Format(time.RFC3339), "2009-02-13T23:31:30Z")
}

func TestAgentIntentArgRewrite(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "json defaults to triage",
			args: []string{"--json"},
			want: []string{"--robot-triage", "--format", "json"},
		},
		{
			name: "json false still avoids tui",
			args: []string{"--json=false"},
			want: []string{"--robot-triage", "--format=json"},
		},
		{
			name: "toon defaults to triage",
			args: []string{"--toon"},
			want: []string{"--robot-triage", "--format", "toon"},
		},
		{
			name: "toon false keeps structured output without forcing toon",
			args: []string{"--toon=false"},
			want: []string{"--robot-triage", "--format=json"},
		},
		{
			name: "output toon defaults to triage",
			args: []string{"--output=toon"},
			want: []string{"--robot-triage", "--format=toon"},
		},
		{
			name: "triage subcommand",
			args: []string{"triage", "--json", "--name", "backend", "--limit", "3"},
			want: []string{"--robot-triage", "--format", "json", "--label", "backend", "--robot-max-results", "3"},
		},
		{
			name: "canonical robot command name",
			args: []string{"robot-triage", "--json"},
			want: []string{"--robot-triage", "--format", "json"},
		},
		{
			name: "canonical robot help with json becomes docs",
			args: []string{"robot-help", "--json"},
			want: []string{"--robot-docs", "guide", "--format", "json"},
		},
		{
			name: "canonical grouped triage command name",
			args: []string{"robot-triage-by-track", "--json", "--limit=2"},
			want: []string{"--robot-triage-by-track", "--format", "json", "--robot-max-results=2"},
		},
		{
			name: "schema subcommand",
			args: []string{"schema", "triage", "--json"},
			want: []string{"--robot-schema", "--schema-command", "robot-triage", "--format", "json"},
		},
		{
			name: "canonical schema command name",
			args: []string{"robot-schema", "triage", "--json"},
			want: []string{"--robot-schema", "--schema-command", "robot-triage", "--format", "json"},
		},
		{
			name: "schema accepts output alias before command",
			args: []string{"schema", "--json", "triage"},
			want: []string{"--robot-schema", "--schema-command", "robot-triage", "--format", "json"},
		},
		{
			name: "search subcommand",
			args: []string{"search", "login", "oauth", "--json", "--limit=5"},
			want: []string{"--search", "login oauth", "--robot-search", "--format", "json", "--search-limit=5"},
		},
		{
			name: "search accepts limit before query",
			args: []string{"search", "--limit", "5", "login", "oauth", "--json"},
			want: []string{"--search", "login oauth", "--robot-search", "--search-limit", "5", "--format", "json"},
		},
		{
			name: "search accepts output alias between query terms",
			args: []string{"search", "login", "--json", "oauth"},
			want: []string{"--search", "login oauth", "--robot-search", "--format", "json"},
		},
		{
			name: "canonical search command name",
			args: []string{"robot-search", "login", "oauth", "--json", "--limit", "5"},
			want: []string{"--search", "login oauth", "--robot-search", "--format", "json", "--search-limit", "5"},
		},
		{
			name: "graph format positional",
			args: []string{"graph", "mermaid", "--output", "json"},
			want: []string{"--robot-graph", "--graph-format", "mermaid", "--format", "json"},
		},
		{
			name: "canonical graph command name",
			args: []string{"robot-graph", "mermaid", "--json"},
			want: []string{"--robot-graph", "--graph-format", "mermaid", "--format", "json"},
		},
		{
			name: "graph accepts output alias before format",
			args: []string{"graph", "--json", "mermaid"},
			want: []string{"--robot-graph", "--graph-format", "mermaid", "--format", "json"},
		},
		{
			name: "related accepts output alias before target",
			args: []string{"related", "--json", "bv-123"},
			want: []string{"--robot-related", "bv-123", "--format", "json"},
		},
		{
			name: "canonical value command name",
			args: []string{"robot-related", "bv-123", "--json", "--limit=2"},
			want: []string{"--robot-related", "bv-123", "--format", "json", "--related-max-results=2"},
		},
		{
			name: "canonical diff command name",
			args: []string{"robot-diff", "HEAD~1", "--json"},
			want: []string{"--robot-diff", "--diff-since", "HEAD~1", "--format", "json"},
		},
		{
			name: "canonical drift command name includes required check",
			args: []string{"robot-drift", "--json"},
			want: []string{"--check-drift", "--robot-drift", "--format", "json"},
		},
		{
			name: "docs accepts output alias before topic",
			args: []string{"docs", "--json", "guide"},
			want: []string{"--robot-docs", "guide", "--format", "json"},
		},
		{
			name: "canonical docs command name",
			args: []string{"robot-docs", "guide", "--json"},
			want: []string{"--robot-docs", "guide", "--format", "json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireArgs(t, rewriteAgentIntentArgs(tt.args), tt.want)
		})
	}
}

func TestAgentIntentAliasesOutputJSON(t *testing.T) {
	tmpDir := t.TempDir()
	beads := `{"id":"A","title":"Root","status":"open","priority":1,"issue_type":"task","labels":["backend"]}
{"id":"B","title":"Blocked","status":"blocked","priority":2,"issue_type":"task","dependencies":[{"depends_on_id":"A","type":"blocks"}]}`
	if err := os.MkdirAll(filepath.Join(tmpDir, ".beads"), 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".beads", "beads.jsonl"), []byte(beads), 0644); err != nil {
		t.Fatalf("write beads dir: %v", err)
	}

	exe := buildTestBinary(t)
	for _, args := range [][]string{
		{"--json"},
		{"robot-help", "--json"},
		{"robot-triage", "--json"},
		{"triage", "--json"},
		{"robot-capabilities", "--json"},
		{"capabilities", "--json"},
		{"robot-docs", "guide", "--json"},
		{"docs", "guide", "--json"},
		{"docs", "--json", "guide"},
		{"robot-schema", "triage", "--json"},
		{"schema", "triage", "--json"},
		{"schema", "--json", "triage"},
		{"robot-graph", "mermaid", "--json"},
		{"graph", "--json", "mermaid"},
		{"--name", "backend", "--json"},
		{"--json=false"},
		{"--toon=false"},
	} {
		stdout, stderr, err := runCommandWithTimeout(t, tmpDir, exe, args...)
		if err != nil {
			t.Fatalf("%v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
		if !json.Valid([]byte(stdout)) {
			t.Fatalf("%v did not return valid JSON\nstdout:\n%s\nstderr:\n%s", args, stdout, stderr)
		}
	}
}

func TestEnumFlagErrorSuggestsNearestValue(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("graph-format", "json", "")
	if err := fs.Set("graph-format", "jsno"); err != nil {
		t.Fatalf("set graph-format: %v", err)
	}

	err := validateEnumFlags(fs, []enumFlagRule{{name: "graph-format", allowed: []string{"json", "dot", "mermaid"}}})
	if err == nil {
		t.Fatal("expected invalid enum error")
	}
	if !strings.Contains(err.Error(), `did you mean "json"?`) {
		t.Fatalf("missing did-you-mean hint: %v", err)
	}
}

func TestResolveSingleRepoWatchFileUsesDiscoveredBeadsJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(`{"id":"legacy"}`+"\n"), 0644); err != nil {
		t.Fatalf("write issues.jsonl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.jsonl"), []byte(`{"id":"canonical"}`+"\n"), 0644); err != nil {
		t.Fatalf("write beads.jsonl: %v", err)
	}

	watchFile, err := resolveSingleRepoWatchFile(tmpDir)
	if err != nil {
		t.Fatalf("resolveSingleRepoWatchFile: %v", err)
	}
	requireString(t, filepath.Base(watchFile), "beads.jsonl")
}

func TestRobotCapabilitiesManifest(t *testing.T) {
	capabilities := generateRobotCapabilities()
	if capabilities["tool"] != "bv" {
		t.Fatalf("tool = %v, want bv", capabilities["tool"])
	}
	if capabilities["contract_version"] != robotContractVersion {
		t.Fatalf("contract_version = %v, want %s", capabilities["contract_version"], robotContractVersion)
	}
	commands, ok := capabilities["commands"].([]map[string]interface{})
	if !ok {
		t.Fatalf("commands has unexpected type %T", capabilities["commands"])
	}
	seen := map[string]map[string]interface{}{}
	for _, command := range commands {
		name, _ := command["name"].(string)
		seen[name] = command
	}
	for name := range primaryRobotFlagNames() {
		if seen[name] == nil {
			t.Fatalf("capabilities missing command %q", name)
		}
	}
	requireString(t, seen["robot-help"]["preferred_invocation"].(string), "bv robot-help --json")
	requireString(t, seen["robot-triage"]["preferred_invocation"].(string), "bv robot-triage --json")
	requireContainsString(t, seen["robot-triage"]["accepted_invocations"].([]string), "bv --robot-triage --format json")
	requireContainsString(t, seen["robot-related"]["accepted_invocations"].([]string), "bv robot-related ISSUE_ID --json")
	if seen["robot-related"]["needs_git"] != true {
		t.Fatalf("robot-related needs_git = %v, want true", seen["robot-related"]["needs_git"])
	}
	if seen["robot-correlation-stats"]["needs_git"] != false {
		t.Fatalf("robot-correlation-stats needs_git = %v, want false", seen["robot-correlation-stats"]["needs_git"])
	}
	if seen["robot-sprint-show"]["preferred_invocation"] != "bv robot-sprint-show SPRINT_ID --json" {
		t.Fatalf("robot-sprint-show preferred_invocation = %v, want SPRINT_ID example", seen["robot-sprint-show"]["preferred_invocation"])
	}
	if seen["robot-sprint-show"]["needs_sprint"] != true {
		t.Fatalf("robot-sprint-show needs_sprint = %v, want true", seen["robot-sprint-show"]["needs_sprint"])
	}
	if seen["robot-drift"]["needs_baseline"] != true {
		t.Fatalf("robot-drift needs_baseline = %v, want true", seen["robot-drift"]["needs_baseline"])
	}
	if seen["robot-confirm-correlation"]["mutates_state"] != true {
		t.Fatalf("robot-confirm-correlation mutates_state = %v, want true", seen["robot-confirm-correlation"]["mutates_state"])
	}
	if seen["robot-reject-correlation"]["mutates_state"] != true {
		t.Fatalf("robot-reject-correlation mutates_state = %v, want true", seen["robot-reject-correlation"]["mutates_state"])
	}
	if seen["robot-explain-correlation"]["mutates_state"] != false {
		t.Fatalf("robot-explain-correlation mutates_state = %v, want false", seen["robot-explain-correlation"]["mutates_state"])
	}
	requireContainsString(t, seen["robot-forecast"]["params"].([]string), "--forecast-sprint SPRINT_ID")
	requireString(t, seen["robot-confirm-correlation"]["preferred_invocation"].(string), "bv robot-confirm-correlation deadbeef:ISSUE_ID --correlation-by agent --json")
	requireString(t, seen["robot-search"]["preferred_invocation"].(string), `bv robot-search "login oauth" --json`)
	requireContainsString(t, seen["robot-search"]["accepted_invocations"].([]string), `bv --search "login oauth" --robot-search --format json`)
	requireString(t, seen["robot-diff"]["preferred_invocation"].(string), "bv robot-diff HEAD~1 --json")
	requireContainsString(t, seen["robot-diff"]["accepted_invocations"].([]string), "bv --robot-diff --diff-since HEAD~1 --format json")
	for _, command := range commands {
		for _, key := range []string{"flag", "preferred_invocation"} {
			value, _ := command[key].(string)
			if strings.ContainsAny(value, "<>") {
				t.Fatalf("%s for %s contains shell redirection placeholder: %q", key, command["name"], value)
			}
		}
		for _, value := range command["accepted_invocations"].([]string) {
			if strings.ContainsAny(value, "<>") {
				t.Fatalf("accepted invocation for %s contains shell redirection placeholder: %q", command["name"], value)
			}
		}
		if params, ok := command["params"].([]string); ok {
			for _, value := range params {
				if strings.ContainsAny(value, "<>") {
					t.Fatalf("param for %s contains shell redirection placeholder: %q", command["name"], value)
				}
			}
		}
	}
	if _, ok := capabilities["environment_variables"].(map[string]string); !ok {
		t.Fatalf("environment_variables has unexpected type %T", capabilities["environment_variables"])
	}
	if _, ok := capabilities["exit_codes"].(map[string]string); !ok {
		t.Fatalf("exit_codes has unexpected type %T", capabilities["exit_codes"])
	}
}

func TestRobotDocsUnknownTopicSuggestsNearestTopic(t *testing.T) {
	docs := generateRobotDocs("guied")
	if docs["did_you_mean"] != "guide" {
		t.Fatalf("did_you_mean = %v, want guide; docs=%v", docs["did_you_mean"], docs)
	}
	if action, _ := docs["suggested_action"].(string); !strings.Contains(action, "bv --robot-docs guide") {
		t.Fatalf("suggested_action missing exact command: %v", docs["suggested_action"])
	}
}

func TestRobotDocsPreferSafeAgentCommandExamples(t *testing.T) {
	guideDocs := generateRobotDocs("guide")
	guide, ok := guideDocs["guide"].(map[string]interface{})
	if !ok {
		t.Fatalf("guide has unexpected type %T", guideDocs["guide"])
	}
	quickstart, ok := guide["quickstart"].([]string)
	if !ok {
		t.Fatalf("quickstart has unexpected type %T", guide["quickstart"])
	}
	requireContainsString(t, quickstart, "bv robot-triage --json           # Full triage with recommendations")
	requireContainsString(t, quickstart, "bv robot-capabilities --json     # Machine-readable command manifest")
	dataSource, ok := guide["data_source"].(string)
	if !ok {
		t.Fatalf("data_source has unexpected type %T", guide["data_source"])
	}
	if !strings.Contains(dataSource, ".beads/beads.jsonl") || !strings.Contains(dataSource, ".beads/issues.jsonl") {
		t.Fatalf("data_source should mention both canonical and compatibility JSONL paths, got %q", dataSource)
	}

	exampleDocs := generateRobotDocs("examples")
	examples, ok := exampleDocs["examples"].([]map[string]string)
	if !ok {
		t.Fatalf("examples has unexpected type %T", exampleDocs["examples"])
	}
	commands := make([]string, 0, len(examples))
	for _, example := range examples {
		command := example["command"]
		commands = append(commands, command)
		if strings.Contains(command, "| sh") {
			t.Fatalf("robot docs example auto-executes shell output: %s", command)
		}
	}
	requireContainsString(t, commands, "bv robot-next --json | jq -r '.claim_command'")
	requireContainsString(t, commands, `bv robot-search "authentication" --json`)
	requireContainsString(t, commands, "BV_OUTPUT_FORMAT=toon bv robot-triage")
	for _, command := range commands {
		if strings.Contains(command, "BV_OUTPUT_FORMAT=toon") && strings.Contains(command, "--json") {
			t.Fatalf("env default example is overridden by --json: %s", command)
		}
	}
}

func TestRobotSchemaCoversDocumentedRobotCommands(t *testing.T) {
	schemas := generateRobotSchemas()
	for name := range robotCommandDocs() {
		if _, ok := schemas.Commands[name]; !ok {
			t.Fatalf("schema missing documented command %q", name)
		}
	}
	for _, name := range []string{"robot-capabilities", "robot-related", "robot-file-hotspots", "robot-impact"} {
		if _, ok := schemas.Commands[name]; !ok {
			t.Fatalf("schema missing %q", name)
		}
	}
}

func TestRobotCapabilitiesSchemaDocumentsCommandMetadata(t *testing.T) {
	schemas := generateRobotSchemas()
	schema := schemas.Commands["robot-capabilities"]
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("robot-capabilities properties has unexpected type %T", schema["properties"])
	}
	if properties["commands"] == nil {
		t.Fatalf("robot-capabilities schema missing commands property")
	}

	commandsProp, ok := properties["commands"].(map[string]interface{})
	if !ok {
		t.Fatalf("commands property has unexpected type %T", properties["commands"])
	}
	items, ok := commandsProp["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("commands items has unexpected type %T", commandsProp["items"])
	}
	commandProperties, ok := items["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("command properties has unexpected type %T", items["properties"])
	}
	for _, name := range []string{"preferred_invocation", "accepted_invocations", "needs_git", "needs_sprint", "needs_baseline", "mutates_state"} {
		if commandProperties[name] == nil {
			t.Fatalf("robot-capabilities command schema missing %q", name)
		}
	}
}

func TestRobotDiffSchemaMatchesHandlerEnvelope(t *testing.T) {
	schemas := generateRobotSchemas()
	schema := schemas.Commands["robot-diff"]
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("robot-diff properties has unexpected type %T", schema["properties"])
	}
	for _, name := range []string{"resolved_revision", "from_data_hash", "to_data_hash", "diff"} {
		if properties[name] == nil {
			t.Fatalf("robot-diff schema missing top-level property %q", name)
		}
	}
	for _, stale := range []string{"since", "since_commit", "new", "closed", "modified", "cycles"} {
		if properties[stale] != nil {
			t.Fatalf("robot-diff schema still exposes stale top-level property %q", stale)
		}
	}

	diffProp, ok := properties["diff"].(map[string]interface{})
	if !ok {
		t.Fatalf("diff property has unexpected type %T", properties["diff"])
	}
	diffProperties, ok := diffProp["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("diff nested properties has unexpected type %T", diffProp["properties"])
	}
	for _, name := range []string{"new_issues", "closed_issues", "removed_issues", "modified_issues", "metric_deltas", "summary"} {
		if diffProperties[name] == nil {
			t.Fatalf("robot-diff nested schema missing %q", name)
		}
	}
}

func TestRobotForecastSchemaMatchesHandlerEnvelope(t *testing.T) {
	schemas := generateRobotSchemas()
	schema := schemas.Commands["robot-forecast"]
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("robot-forecast properties has unexpected type %T", schema["properties"])
	}
	for _, name := range []string{"agents", "filters", "forecast_count", "forecasts", "summary", "output_format", "version"} {
		if properties[name] == nil {
			t.Fatalf("robot-forecast schema missing top-level property %q", name)
		}
	}
	if properties["methodology"] != nil {
		t.Fatalf("robot-forecast schema still exposes stale methodology property")
	}

	summaryProp, ok := properties["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("summary property has unexpected type %T", properties["summary"])
	}
	summaryProperties, ok := summaryProp["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("summary nested properties has unexpected type %T", summaryProp["properties"])
	}
	for _, name := range []string{"total_minutes", "total_days", "avg_confidence", "earliest_eta", "latest_eta"} {
		if summaryProperties[name] == nil {
			t.Fatalf("robot-forecast summary schema missing %q", name)
		}
	}
}

func TestRobotBurndownSchemaMatchesHandlerEnvelope(t *testing.T) {
	schemas := generateRobotSchemas()
	schema := schemas.Commands["robot-burndown"]
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("robot-burndown properties has unexpected type %T", schema["properties"])
	}
	for _, name := range []string{
		"output_format", "version", "sprint_name", "start_date", "end_date",
		"total_days", "elapsed_days", "remaining_days",
		"total_issues", "completed_issues", "remaining_issues",
		"ideal_burn_rate", "actual_burn_rate", "projected_complete",
		"on_track", "daily_points", "ideal_line",
	} {
		if properties[name] == nil {
			t.Fatalf("robot-burndown schema missing top-level property %q", name)
		}
	}
	for _, stale := range []string{"burndown", "at_risk"} {
		if properties[stale] != nil {
			t.Fatalf("robot-burndown schema still exposes stale top-level property %q", stale)
		}
	}

	dailyPoints, ok := properties["daily_points"].(map[string]interface{})
	if !ok {
		t.Fatalf("daily_points property has unexpected type %T", properties["daily_points"])
	}
	items, ok := dailyPoints["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("daily_points items has unexpected type %T", dailyPoints["items"])
	}
	pointProperties, ok := items["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("daily_points item properties has unexpected type %T", items["properties"])
	}
	for _, name := range []string{"date", "remaining", "completed"} {
		if pointProperties[name] == nil {
			t.Fatalf("robot-burndown point schema missing %q", name)
		}
	}
}

func TestRobotGraphSchemaMatchesExportResult(t *testing.T) {
	schemas := generateRobotSchemas()
	schema := schemas.Commands["robot-graph"]
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("robot-graph properties has unexpected type %T", schema["properties"])
	}
	for _, name := range []string{"format", "graph", "nodes", "edges", "filters_applied", "explanation", "data_hash", "adjacency"} {
		if properties[name] == nil {
			t.Fatalf("robot-graph schema missing top-level property %q", name)
		}
	}
	for _, stale := range []string{"generated_at", "stats"} {
		if properties[stale] != nil {
			t.Fatalf("robot-graph schema still exposes stale top-level property %q", stale)
		}
	}

	explanation, ok := properties["explanation"].(map[string]interface{})
	if !ok {
		t.Fatalf("explanation property has unexpected type %T", properties["explanation"])
	}
	explanationProperties, ok := explanation["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("explanation nested properties has unexpected type %T", explanation["properties"])
	}
	for _, name := range []string{"what", "how_to_render", "when_to_use"} {
		if explanationProperties[name] == nil {
			t.Fatalf("robot-graph explanation schema missing %q", name)
		}
	}
}

func TestModifierFlagValidation(t *testing.T) {
	exe := buildTestBinary(t)
	tmpDir := t.TempDir()

	tests := []struct {
		name         string
		args         []string
		wantMessages []string
	}{
		{
			name: "robot diff requires diff since",
			args: []string{"--robot-diff"},
			wantMessages: []string{
				"Error: --robot-diff requires --diff-since",
				"Try one of:",
				"`bv robot-diff HEAD~1 --json`",
				"`bv --robot-diff --diff-since HEAD~1 --format json`",
			},
		},
		{
			name: "robot search requires search query",
			args: []string{"robot-search", "--json"},
			wantMessages: []string{
				"Error: --robot-search requires --search",
				"Try one of:",
				"`bv robot-search \"login oauth\" --json`",
				"`bv --search \"login oauth\" --robot-search --format json`",
			},
		},
		{
			name: "graph format requires graph command",
			args: []string{"--graph-format", "mermaid"},
			wantMessages: []string{
				"Error: --graph-format requires --robot-graph",
				"Try: `bv robot-graph mermaid --json`.",
			},
		},
		{
			name: "robot drift requires check drift",
			args: []string{"--robot-drift"},
			wantMessages: []string{
				"Error: --robot-drift requires --check-drift",
				"Try: `bv --check-drift --robot-drift --format json`.",
			},
		},
		{
			name: "schema command requires robot schema",
			args: []string{"--schema-command", "robot-triage"},
			wantMessages: []string{
				"Error: --schema-command requires --robot-schema",
				"Try: `bv robot-schema triage --json`.",
			},
		},
		{
			name: "watch export requires export pages",
			args: []string{"--watch-export"},
			wantMessages: []string{
				"Error: --watch-export requires --export-pages",
				"Try: `bv --export-pages ./bv-pages --watch-export`.",
			},
		},
		{
			name: "history since requires history mode",
			args: []string{"--history-since", "30 days ago"},
			wantMessages: []string{
				"Error: --history-since requires one of --robot-history or --bead-history",
				"Try: `bv robot-history --history-since \"30 days ago\" --json`.",
			},
		},
		{
			name: "capacity agents requires robot capacity",
			args: []string{"--agents", "3"},
			wantMessages: []string{
				"Error: --agents requires --robot-capacity",
				"Try: `bv robot-capacity --agents 3 --json`.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, err := runCommandWithTimeout(t, tmpDir, exe, tt.args...)
			if err == nil {
				t.Fatalf("expected %v to fail, got success\nstdout:\n%s\nstderr:\n%s", tt.args, stdout, stderr)
			}

			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("expected ExitError for %v, got %T", tt.args, err)
			}
			if exitErr.ExitCode() != 1 {
				t.Fatalf("exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", exitErr.ExitCode(), stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("expected empty stdout for %v, got:\n%s", tt.args, stdout)
			}
			for _, want := range tt.wantMessages {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr missing %q\nfull stderr:\n%s", want, stderr)
				}
			}
		})
	}
}

func TestApplyRecipeFilters_ActionableAndHasBlockers(t *testing.T) {
	now := time.Now()
	a := model.Issue{ID: "A", Title: "Root", Status: model.StatusOpen, Priority: 2, CreatedAt: now}
	b := model.Issue{
		ID:     "B",
		Title:  "Blocked by A",
		Status: model.StatusOpen,
		Dependencies: []*model.Dependency{
			{DependsOnID: "A", Type: model.DepBlocks},
		},
		CreatedAt: now.Add(-time.Hour),
	}
	issues := []model.Issue{a, b}

	r := &recipe.Recipe{
		Filters: recipe.FilterConfig{
			Actionable: ptrBool(true),
		},
	}
	actionable := applyRecipeFilters(issues, r)
	requireIssueIDs(t, actionable, "A")

	r.Filters.Actionable = nil
	r.Filters.HasBlockers = ptrBool(true)
	blocked := applyRecipeFilters(issues, r)
	requireIssueIDs(t, blocked, "B")
}

func TestApplyRecipeFilters_TitleAndPrefix(t *testing.T) {
	issues := []model.Issue{
		{ID: "UI-1", Title: "Add login button"},
		{ID: "API-2", Title: "Login endpoint"},
		{ID: "API-3", Title: "Health check"},
	}
	r := &recipe.Recipe{
		Filters: recipe.FilterConfig{
			TitleContains: "login",
			IDPrefix:      "API",
		},
	}
	got := applyRecipeFilters(issues, r)
	requireIssueIDs(t, got, "API-2")
}

func TestApplyRecipeFilters_TagsAndDates(t *testing.T) {
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	issues := []model.Issue{
		{ID: "T1", Title: "Tagged", Labels: []string{"backend", "p0"}, CreatedAt: now, UpdatedAt: now},
		{ID: "T2", Title: "Old", Labels: []string{"backend"}, CreatedAt: old, UpdatedAt: old},
	}
	r := &recipe.Recipe{
		Filters: recipe.FilterConfig{
			Tags:         []string{"backend"},
			ExcludeTags:  []string{"p0"},
			CreatedAfter: "1d",
			UpdatedAfter: "1d",
		},
	}
	got := applyRecipeFilters(issues, r)
	if len(got) != 0 {
		t.Fatalf("expected all filtered out (exclude p0 and date), got %#v", got)
	}
}

func TestApplyRecipeFilters_DatesBlockersAndPrefix(t *testing.T) {
	now := time.Now()
	early := now.Add(-72 * time.Hour)
	issues := []model.Issue{
		{ID: "API-1", Title: "Fresh", CreatedAt: now, UpdatedAt: now},
		{ID: "API-2", Title: "Stale", CreatedAt: early, UpdatedAt: early,
			Dependencies: []*model.Dependency{{DependsOnID: "API-1", Type: model.DepBlocks}}},
	}
	r := &recipe.Recipe{Filters: recipe.FilterConfig{
		CreatedBefore: "1h",
		UpdatedBefore: "1h",
		HasBlockers:   ptrBool(true),
		IDPrefix:      "API-2",
	}}
	got := applyRecipeFilters(issues, r)
	requireIssueIDs(t, got, "API-2")

	r.Filters.HasBlockers = ptrBool(false)
	got = applyRecipeFilters(issues, r)
	if len(got) != 0 {
		t.Fatalf("expected blockers=false to exclude API-2, got %#v", got)
	}
}

func TestApplyRecipeSort_DefaultsAndFields(t *testing.T) {
	now := time.Now()
	issues := []model.Issue{
		{ID: "A", Title: "zzz", Priority: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-30 * time.Minute)},
		{ID: "B", Title: "aaa", Priority: 0, CreatedAt: now, UpdatedAt: now},
	}

	// Priority default ascending
	r := &recipe.Recipe{Sort: recipe.SortConfig{Field: "priority"}}
	sorted := applyRecipeSort(append([]model.Issue{}, issues...), r)
	requireIssueIDs(t, sorted[:1], "B")

	// Created default descending (newest first)
	r.Sort = recipe.SortConfig{Field: "created"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	requireIssueIDs(t, sorted[:1], "B")

	// Title ascending explicit desc
	r.Sort = recipe.SortConfig{Field: "title", Direction: "desc"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	requireIssueIDs(t, sorted[:1], "A")

	// Status ascending (string compare)
	r.Sort = recipe.SortConfig{Field: "status"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	requireIssueIDs(t, sorted[:1], "A")

	// ID natural sort
	idIssues := []model.Issue{
		{ID: "bv-10"},
		{ID: "bv-2"},
		{ID: "bv-1"},
	}
	r.Sort = recipe.SortConfig{Field: "id"}
	sortedIDs := applyRecipeSort(append([]model.Issue{}, idIssues...), r)
	requireIssueIDs(t, sortedIDs, "bv-1", "bv-2", "bv-10")

	// Unknown field should preserve order
	r.Sort = recipe.SortConfig{Field: "unknown"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	requireIssueIDs(t, sorted, "A", "B")
}

func TestFormatCycle(t *testing.T) {
	requireString(t, formatCycle(nil), "(empty)")
	c := []string{"X", "Y", "Z"}
	want := "X → Y → Z → X"
	requireString(t, formatCycle(c), want)
}

func ptrBool(b bool) *bool { return &b }

func requireIssueIDs(t *testing.T, issues []model.Issue, want ...string) {
	t.Helper()
	if len(issues) != len(want) {
		t.Fatalf("issue count = %d, want %d; issues=%#v", len(issues), len(want), issues)
	}
	for i := range want {
		if strings.Compare(issues[i].ID, want[i]) != 0 {
			t.Fatalf("issue[%d].ID = %q, want %q; issues=%#v", i, issues[i].ID, want[i], issues)
		}
	}
}

func requireString(t *testing.T, got, want string) {
	t.Helper()
	if strings.Compare(got, want) != 0 {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func requireContainsString(t *testing.T, got []string, want string) {
	t.Helper()
	for _, value := range got {
		if strings.Compare(value, want) == 0 {
			return
		}
	}
	t.Fatalf("%#v does not contain %q", got, want)
}

func requireArgs(t *testing.T, got, want []string) {
	t.Helper()
	gotJoined := strings.Join(got, "\x00")
	wantJoined := strings.Join(want, "\x00")
	if strings.Compare(gotJoined, wantJoined) != 0 {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func writeTestBeadsFixture(t *testing.T, dir string) {
	t.Helper()

	beads := `{"id":"A","title":"Root","status":"open","priority":1,"issue_type":"task","labels":["backend"]}
{"id":"B","title":"Blocked","status":"blocked","priority":2,"issue_type":"task","labels":["backend"],"dependencies":[{"depends_on_id":"A","type":"blocks"}]}
{"id":"C","title":"UI","status":"open","priority":2,"issue_type":"task","labels":["frontend"]}`

	if err := os.WriteFile(filepath.Join(dir, ".beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".beads", "beads.jsonl"), []byte(beads), 0o644); err != nil {
		t.Fatalf("write beads dir: %v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod above %s", dir)
		}
		dir = parent
	}
}
