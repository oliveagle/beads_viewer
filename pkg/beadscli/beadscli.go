// Package beadscli manages process-local state for the active Beads CLI tool.
// bv emits shell commands referencing either "br" (file-first beads_rust stack)
// or "bd" (modern Dolt-native workspaces). This package centralises that
// selection so every code path that renders a CLI command uses the same binary
// name without threading a config parameter everywhere.
package beadscli

import (
	"fmt"
	"strings"
)

// current tracks the active Beads CLI for this process.
// It is internal runtime state, not a user-facing config contract.
var current = "br"

// SetTool sets the active Beads CLI for generated helper commands.
// Only "bd" is treated as a distinct backend; everything else defaults to "br".
func SetTool(tool string) {
	switch strings.TrimSpace(strings.ToLower(tool)) {
	case "bd":
		current = "bd"
	default:
		current = "br"
	}
}

// Tool returns the active command binary name ("br" or "bd").
func Tool() string {
	return current
}

// Shell formats a plain shell command for the active backend.
// The placeholder {tool} in the format string is replaced with the current
// tool name before standard fmt.Sprintf interpolation.
func Shell(format string, args ...any) string {
	return fmt.Sprintf(strings.ReplaceAll(format, "{tool}", Tool()), args...)
}

// CI formats a CI-prefixed shell command for the active backend.
func CI(format string, args ...any) string {
	return "CI=1 " + Shell(format, args...)
}
