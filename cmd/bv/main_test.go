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

func TestModifierFlagValidation(t *testing.T) {
	exe := buildTestBinary(t)
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{
			name:        "robot diff requires diff since",
			args:        []string{"--robot-diff"},
			wantMessage: "Error: --robot-diff requires --diff-since",
		},
		{
			name:        "robot drift requires check drift",
			args:        []string{"--robot-drift"},
			wantMessage: "Error: --robot-drift requires --check-drift",
		},
		{
			name:        "schema command requires robot schema",
			args:        []string{"--schema-command", "robot-triage"},
			wantMessage: "Error: --schema-command requires --robot-schema",
		},
		{
			name:        "watch export requires export pages",
			args:        []string{"--watch-export"},
			wantMessage: "Error: --watch-export requires --export-pages",
		},
		{
			name:        "history since requires history mode",
			args:        []string{"--history-since", "30 days ago"},
			wantMessage: "Error: --history-since requires one of --robot-history or --bead-history",
		},
		{
			name:        "capacity agents requires robot capacity",
			args:        []string{"--agents", "3"},
			wantMessage: "Error: --agents requires --robot-capacity",
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
			if !strings.Contains(stderr, tt.wantMessage) {
				t.Fatalf("stderr missing %q\nfull stderr:\n%s", tt.wantMessage, stderr)
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
	if len(actionable) != 1 || actionable[0].ID != "A" {
		t.Fatalf("expected only A actionable, got %#v", actionable)
	}

	r.Filters.Actionable = nil
	r.Filters.HasBlockers = ptrBool(true)
	blocked := applyRecipeFilters(issues, r)
	if len(blocked) != 1 || blocked[0].ID != "B" {
		t.Fatalf("expected only B when HasBlockers=true, got %#v", blocked)
	}
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
	if len(got) != 1 || got[0].ID != "API-2" {
		t.Fatalf("expected API-2 only, got %#v", got)
	}
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
	if len(got) != 1 || got[0].ID != "API-2" {
		t.Fatalf("expected only API-2 to match blockers/date/prefix filters, got %#v", got)
	}

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
	if sorted[0].ID != "B" {
		t.Fatalf("priority sort expected B first, got %s", sorted[0].ID)
	}

	// Created default descending (newest first)
	r.Sort = recipe.SortConfig{Field: "created"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	if sorted[0].ID != "B" {
		t.Fatalf("created sort expected newest (B) first, got %s", sorted[0].ID)
	}

	// Title ascending explicit desc
	r.Sort = recipe.SortConfig{Field: "title", Direction: "desc"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	if sorted[0].ID != "A" {
		t.Fatalf("title desc expected A (zzz) first, got %s", sorted[0].ID)
	}

	// Status ascending (string compare)
	r.Sort = recipe.SortConfig{Field: "status"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	if sorted[0].ID != "A" { // both open; stable sort keeps original order
		t.Fatalf("status sort expected A first, got %s", sorted[0].ID)
	}

	// ID natural sort
	idIssues := []model.Issue{
		{ID: "bv-10"},
		{ID: "bv-2"},
		{ID: "bv-1"},
	}
	r.Sort = recipe.SortConfig{Field: "id"}
	sortedIDs := applyRecipeSort(append([]model.Issue{}, idIssues...), r)
	if sortedIDs[0].ID != "bv-1" || sortedIDs[1].ID != "bv-2" || sortedIDs[2].ID != "bv-10" {
		t.Fatalf("id natural sort failed: got %v", []string{sortedIDs[0].ID, sortedIDs[1].ID, sortedIDs[2].ID})
	}

	// Unknown field should preserve order
	r.Sort = recipe.SortConfig{Field: "unknown"}
	sorted = applyRecipeSort(append([]model.Issue{}, issues...), r)
	if sorted[0].ID != "A" || sorted[1].ID != "B" {
		t.Fatalf("unknown sort field should keep original order, got %v", []string{sorted[0].ID, sorted[1].ID})
	}
}

func TestFormatCycle(t *testing.T) {
	if got := formatCycle(nil); got != "(empty)" {
		t.Fatalf("expected (empty), got %q", got)
	}
	c := []string{"X", "Y", "Z"}
	want := "X → Y → Z → X"
	if got := formatCycle(c); got != want {
		t.Fatalf("formatCycle mismatch: got %q want %q", got, want)
	}
}

func ptrBool(b bool) *bool { return &b }

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
