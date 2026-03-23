package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRobotRegistryValidate_RejectsModifierAlone(t *testing.T) {
	var robotTriage bool
	robotByLabel := "backend"

	registry := newRobotRegistry()
	registry.Register(RobotCommand{
		Name:        "robot-triage",
		FlagName:    "robot-triage",
		FlagPtr:     &robotTriage,
		Description: "Unified triage output",
	})
	registry.Register(RobotCommand{
		Name:            "robot-by-label",
		FlagName:        "robot-by-label",
		FlagPtr:         &robotByLabel,
		RequiredCoFlags: []string{"robot-triage", "robot-insights", "robot-plan", "robot-priority"},
		IsModifier:      true,
		Description:     "Filter robot output by label",
	})
	registry.Register(RobotCommand{
		Name:        "robot-insights",
		FlagName:    "robot-insights",
		FlagPtr:     ptrTo(false),
		Description: "Insights output",
	})
	registry.Register(RobotCommand{
		Name:        "robot-plan",
		FlagName:    "robot-plan",
		FlagPtr:     ptrTo(false),
		Description: "Plan output",
	})
	registry.Register(RobotCommand{
		Name:        "robot-priority",
		FlagName:    "robot-priority",
		FlagPtr:     ptrTo(false),
		Description: "Priority output",
	})

	err := registry.Validate()
	if err == nil {
		t.Fatal("expected modifier-alone validation error")
	}
	if !strings.Contains(err.Error(), "--robot-by-label") {
		t.Fatalf("expected error to mention modifier flag, got %q", err)
	}
	if !strings.Contains(err.Error(), "--robot-triage") {
		t.Fatalf("expected error to mention required co-flag, got %q", err)
	}

	robotTriage = true
	if err := registry.Validate(); err != nil {
		t.Fatalf("expected modifier to validate once paired with primary flag: %v", err)
	}
}

func TestRobotRegistryAnyActive_MatchesOldLogic(t *testing.T) {
	var (
		robotHelp       bool
		robotInsights   bool
		robotTriage     bool
		robotSearch     bool
		robotFileBeads  string
		robotByLabel    string
		robotByAssignee string
		robotDocs       string
	)

	registry := newRobotRegistry()
	registry.Register(RobotCommand{Name: "robot-help", FlagName: "robot-help", FlagPtr: &robotHelp, Description: "Help"})
	registry.Register(RobotCommand{Name: "robot-insights", FlagName: "robot-insights", FlagPtr: &robotInsights, Description: "Insights"})
	registry.Register(RobotCommand{Name: "robot-triage", FlagName: "robot-triage", FlagPtr: &robotTriage, Description: "Triage"})
	registry.Register(RobotCommand{Name: "robot-search", FlagName: "robot-search", FlagPtr: &robotSearch, Description: "Search"})
	registry.Register(RobotCommand{Name: "robot-file-beads", FlagName: "robot-file-beads", FlagPtr: &robotFileBeads, Description: "File beads"})
	registry.Register(RobotCommand{
		Name:            "robot-by-label",
		FlagName:        "robot-by-label",
		FlagPtr:         &robotByLabel,
		RequiredCoFlags: []string{"robot-insights", "robot-triage"},
		IsModifier:      true,
		Description:     "Label filter",
	})
	registry.Register(RobotCommand{
		Name:            "robot-by-assignee",
		FlagName:        "robot-by-assignee",
		FlagPtr:         &robotByAssignee,
		RequiredCoFlags: []string{"robot-insights", "robot-triage"},
		IsModifier:      true,
		Description:     "Assignee filter",
	})
	registry.Register(RobotCommand{Name: "robot-docs", FlagName: "robot-docs", FlagPtr: &robotDocs, Description: "Docs"})

	oldLogic := func() bool {
		return robotHelp ||
			robotInsights ||
			robotTriage ||
			robotSearch ||
			robotFileBeads != "" ||
			robotByLabel != "" ||
			robotByAssignee != "" ||
			robotDocs != ""
	}

	tests := []struct {
		name  string
		setup func()
	}{
		{name: "none active", setup: func() {}},
		{name: "help active", setup: func() { robotHelp = true }},
		{name: "primary robot command active", setup: func() { robotTriage = true }},
		{name: "string command active", setup: func() { robotFileBeads = "pkg/ui/model.go" }},
		{name: "modifier alone still enables robot mode", setup: func() { robotByLabel = "backend" }},
		{name: "docs topic active", setup: func() { robotDocs = "commands" }},
		{name: "multiple mixed flags", setup: func() {
			robotSearch = true
			robotByAssignee = "alice"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			robotHelp = false
			robotInsights = false
			robotTriage = false
			robotSearch = false
			robotFileBeads = ""
			robotByLabel = ""
			robotByAssignee = ""
			robotDocs = ""

			tt.setup()

			if got, want := registry.AnyActive(), oldLogic(); got != want {
				t.Fatalf("AnyActive()=%v, want %v", got, want)
			}
		})
	}
}

func TestRobotRegistryDispatchFlag_RunsActiveHandler(t *testing.T) {
	var robotHelp bool
	var called int

	registry := newRobotRegistry()
	registry.Register(RobotCommand{
		Name:     "robot-help",
		FlagName: "robot-help",
		FlagPtr:  &robotHelp,
		Handler: func(ctx RobotContext) error {
			called++
			if got := ctx.StdoutOrDefault(); got != ctx.Stdout {
				t.Fatalf("expected dispatch to preserve stdout writer")
			}
			return nil
		},
	})

	stdout := &bytes.Buffer{}
	ctx := RobotContext{Stdout: stdout}

	handled, err := registry.DispatchFlag("robot-help", ctx)
	if err != nil {
		t.Fatalf("inactive flag should not error: %v", err)
	}
	if handled {
		t.Fatal("inactive flag should not dispatch")
	}

	robotHelp = true
	handled, err = registry.DispatchFlag("robot-help", ctx)
	if err != nil {
		t.Fatalf("dispatch returned error: %v", err)
	}
	if !handled {
		t.Fatal("active flag should dispatch")
	}
	if called != 1 {
		t.Fatalf("handler call count = %d, want 1", called)
	}
}

func TestDispatchRobotFlagResult_ReturnsComposableOutcome(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var robotHelp bool

		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return nil
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if result.Handled {
			t.Fatal("inactive flag should not dispatch")
		}
		if result.ExitCode != 0 {
			t.Fatalf("inactive flag exit code = %d, want 0", result.ExitCode)
		}

		robotHelp = true
		result = dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 0 {
			t.Fatalf("successful dispatch exit code = %d, want 0", result.ExitCode)
		}
		if result.Err != nil {
			t.Fatalf("successful dispatch should not return error: %v", result.Err)
		}
		if result.AlreadyReported {
			t.Fatal("successful dispatch should not be marked reported")
		}
	})

	t.Run("handler error", func(t *testing.T) {
		var robotHelp = true
		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return errors.New("boom")
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 1 {
			t.Fatalf("error dispatch exit code = %d, want 1", result.ExitCode)
		}
		if result.Err == nil || !strings.Contains(result.Err.Error(), "boom") {
			t.Fatalf("error dispatch returned err = %v, want boom", result.Err)
		}
		if result.AlreadyReported {
			t.Fatal("plain handler errors should not be marked reported")
		}
	})

	t.Run("reported exit", func(t *testing.T) {
		var robotHelp = true
		registry := newRobotRegistry()
		registry.Register(RobotCommand{
			Name:     "robot-help",
			FlagName: "robot-help",
			FlagPtr:  &robotHelp,
			Handler: func(RobotContext) error {
				return newReportedRobotHandlerExit(2)
			},
		})

		result := dispatchRobotFlagResult(&registry, "robot-help", RobotContext{})
		if !result.Handled {
			t.Fatal("active flag should dispatch")
		}
		if result.ExitCode != 2 {
			t.Fatalf("reported dispatch exit code = %d, want 2", result.ExitCode)
		}
		if result.Err != nil {
			t.Fatalf("reported exit should not retain wrapped error: %v", result.Err)
		}
		if !result.AlreadyReported {
			t.Fatal("reported exit should preserve AlreadyReported")
		}
	})
}

func TestWriteRobotHelp_ReturnsWriterError(t *testing.T) {
	err := writeRobotHelp(failingWriter{err: errors.New("write failed")})
	if err == nil {
		t.Fatal("expected writer error")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected wrapped writer error, got %v", err)
	}
}

func TestWriteRobotHelp_ReturnsWriterErrorAfterIntro(t *testing.T) {
	writer := &failAfterNWritesWriter{
		failAfter: 1,
		err:       errors.New("write failed after intro"),
	}

	err := writeRobotHelp(writer)
	if err == nil {
		t.Fatal("expected writer error after intro")
	}
	if !strings.Contains(err.Error(), "key bindings") {
		t.Fatalf("expected contextual error for later write, got %v", err)
	}
	if !strings.Contains(err.Error(), "write failed after intro") {
		t.Fatalf("expected underlying writer error, got %v", err)
	}
}

func ptrTo[T any](v T) *T {
	return &v
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type failAfterNWritesWriter struct {
	failAfter int
	writes    int
	err       error
}

func (w *failAfterNWritesWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, w.err
	}
	w.writes++
	return len(p), nil
}
