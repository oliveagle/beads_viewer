package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Dicklesworthstone/beads_viewer/pkg/analysis"
	"github.com/Dicklesworthstone/beads_viewer/pkg/baseline"
	"github.com/Dicklesworthstone/beads_viewer/pkg/drift"
	"github.com/Dicklesworthstone/beads_viewer/pkg/export"
	"github.com/Dicklesworthstone/beads_viewer/pkg/loader"
	"github.com/Dicklesworthstone/beads_viewer/pkg/metrics"
	"github.com/Dicklesworthstone/beads_viewer/pkg/model"
	"github.com/Dicklesworthstone/beads_viewer/pkg/recipe"
	"github.com/Dicklesworthstone/beads_viewer/pkg/ui"
	"github.com/Dicklesworthstone/beads_viewer/pkg/version"
)

type RobotCommand struct {
	Name            string
	FlagName        string
	FlagPtr         interface{}
	RequiredCoFlags []string
	IsModifier      bool
	Handler         func(RobotContext) error
	Description     string
}

type RobotContext struct {
	Issues               []model.Issue
	DataHash             string
	Encoder              robotEncoder
	AsOf                 string
	AsOfCommit           string
	LabelScope           string
	LabelContext         *analysis.LabelHealth
	Stdout               io.Writer
	Stderr               io.Writer
	WorkDir              string
	ProjectDir           string
	BaselinePath         string
	EnvRobot             bool
	SearchOutput         *robotSearchOutput
	Diff                 *analysis.SnapshotDiff
	DiffHistoricalIssues []model.Issue
	DiffResolvedRevision string
}

type RobotRegistry struct {
	commands []RobotCommand
}

var robotRegistry = newRobotRegistry()

type robotHandlerExitError struct {
	ExitCode        int
	Err             error
	AlreadyReported bool
}

func (e *robotHandlerExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("robot handler exit %d", e.ExitCode)
}

func (e *robotHandlerExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type phaseOneRobotHandlerConfig struct {
	RobotHelpFlag    *bool
	RobotSchemaFlag  *bool
	RobotRecipesFlag *bool
	RobotMetricsFlag *bool
	VersionFlag      *bool
	SchemaCommand    *string
	RecipeLoader     func() *recipe.Loader
}

type phaseTwoRobotHandlerConfig struct {
	RobotPlanFlag       *bool
	RobotPriorityFlag   *bool
	RobotAlertsFlag     *bool
	RobotSuggestFlag    *bool
	RobotBurndownFlag   *string
	RobotForecastFlag   *string
	RobotSprintListFlag *bool
	RobotGraphFlag      *bool
	RobotSearchFlag     *bool
	RobotDiffFlag       *bool
	ForceFullAnalysis   *bool
	GraphFormat         *string
	GraphRoot           *string
	GraphDepth          *int
	AlertSeverity       *string
	AlertType           *string
	AlertLabel          *string
	SuggestType         *string
	SuggestConfidence   *float64
	SuggestBead         *string
	RobotMinConf        *float64
	RobotMaxResults     *int
	RobotByLabel        *string
	RobotByAssignee     *string
	ForecastLabel       *string
	ForecastSprint      *string
	ForecastAgents      *int
}

func newRobotRegistry() RobotRegistry {
	return RobotRegistry{}
}

func (ctx RobotContext) StdoutOrDefault() io.Writer {
	if ctx.Stdout != nil {
		return ctx.Stdout
	}
	return os.Stdout
}

func (ctx RobotContext) StderrOrDefault() io.Writer {
	if ctx.Stderr != nil {
		return ctx.Stderr
	}
	return os.Stderr
}

func (ctx RobotContext) EncoderOrDefault() robotEncoder {
	if ctx.Encoder != nil {
		return ctx.Encoder
	}
	return newRobotEncoder(ctx.StdoutOrDefault())
}

func (ctx RobotContext) WorkDirOrDefault() (string, error) {
	if strings.TrimSpace(ctx.WorkDir) != "" {
		return ctx.WorkDir, nil
	}
	return os.Getwd()
}

func (ctx RobotContext) ProjectDirOrDefault() (string, error) {
	if strings.TrimSpace(ctx.ProjectDir) != "" {
		return ctx.ProjectDir, nil
	}
	return ctx.WorkDirOrDefault()
}

func (ctx RobotContext) BaselinePathOrDefault() (string, error) {
	if strings.TrimSpace(ctx.BaselinePath) != "" {
		return ctx.BaselinePath, nil
	}
	projectDir, err := ctx.ProjectDirOrDefault()
	if err != nil {
		return "", err
	}
	return baseline.DefaultPath(projectDir), nil
}

func (r *RobotRegistry) Register(cmd RobotCommand) {
	cmd.Name = strings.TrimSpace(cmd.Name)
	cmd.FlagName = normalizeRobotFlagName(cmd.FlagName)
	cmd.RequiredCoFlags = normalizeRobotFlagNames(cmd.RequiredCoFlags)

	if cmd.Name == "" {
		panic("robot command name must not be empty")
	}
	if cmd.FlagName == "" {
		panic("robot command flag name must not be empty")
	}
	if cmd.FlagPtr == nil {
		panic(fmt.Sprintf("robot command %q has nil FlagPtr", cmd.Name))
	}
	for _, existing := range r.commands {
		if strings.EqualFold(existing.Name, cmd.Name) {
			panic(fmt.Sprintf("robot command %q registered twice", cmd.Name))
		}
		if strings.EqualFold(existing.FlagName, cmd.FlagName) {
			panic(fmt.Sprintf("robot flag %q registered twice", formatRobotFlag(cmd.FlagName)))
		}
	}

	r.commands = append(r.commands, cmd)
}

func (r *RobotRegistry) AnyActive() bool {
	for _, cmd := range r.commands {
		if robotFlagActive(cmd.FlagPtr) {
			return true
		}
	}
	return false
}

func (r *RobotRegistry) ActiveCommands() []RobotCommand {
	active := make([]RobotCommand, 0, len(r.commands))
	for _, cmd := range r.commands {
		if robotFlagActive(cmd.FlagPtr) {
			active = append(active, cmd)
		}
	}
	return active
}

func (r *RobotRegistry) DispatchFlag(flagName string, ctx RobotContext) (bool, error) {
	normalized := normalizeRobotFlagName(flagName)
	if normalized == "" {
		return false, nil
	}

	for _, cmd := range r.commands {
		if cmd.FlagName != normalized || !robotFlagActive(cmd.FlagPtr) {
			continue
		}
		if cmd.Handler == nil {
			return true, fmt.Errorf("robot command %q has no handler", cmd.Name)
		}
		return true, cmd.Handler(ctx)
	}

	return false, nil
}

func dispatchRobotFlagOrExit(registry *RobotRegistry, flagName string, ctx RobotContext) {
	if registry == nil {
		return
	}

	handled, err := registry.DispatchFlag(flagName, ctx)
	if !handled {
		return
	}
	if err == nil {
		os.Exit(0)
	}

	exitCode := 1
	reported := false
	var handlerErr *robotHandlerExitError
	if errors.As(err, &handlerErr) {
		if handlerErr.ExitCode != 0 {
			exitCode = handlerErr.ExitCode
		}
		reported = handlerErr.AlreadyReported
		err = handlerErr.Err
	}

	if !reported {
		if err != nil {
			fmt.Fprintf(ctx.StderrOrDefault(), "Error handling %s: %v\n", formatRobotFlag(flagName), err)
		} else {
			fmt.Fprintf(ctx.StderrOrDefault(), "Error handling %s\n", formatRobotFlag(flagName))
		}
	}

	os.Exit(exitCode)
}

func newReportedRobotHandlerExit(exitCode int) error {
	return &robotHandlerExitError{
		ExitCode:        exitCode,
		AlreadyReported: true,
	}
}

// writeRobotHelp outputs the robot help documentation.
func writeRobotHelp(out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}

	_, err := io.WriteString(out, `bv (Beads Viewer) AI Agent Interface
====================================
Use --robot-* flags for deterministic automation output.
Bare bv launches the interactive TUI.

Core commands:
  --robot-triage    Unified triage output (recommended entry point)
  --robot-next      Single top recommendation
  --robot-plan      Dependency-respecting execution tracks
  --robot-insights  Graph metrics and structural analysis

`)
	if err != nil {
		return err
	}

	// Key bindings table (bv-xl6g)
	fmt.Fprintln(out, "TUI Key Bindings:")
	fmt.Fprintln(out, "-----------------")
	bindings := ui.GetKeyBindingDocs()

	// Group by category
	categories := make(map[string][]ui.KeyBindingDoc)
	categoryOrder := []string{}
	for _, b := range bindings {
		if _, exists := categories[b.Category]; !exists {
			categoryOrder = append(categoryOrder, b.Category)
		}
		categories[b.Category] = append(categories[b.Category], b)
	}

	for _, cat := range categoryOrder {
		fmt.Fprintf(out, "\n[%s]\n", cat)
		for _, b := range categories[cat] {
			fmt.Fprintf(out, "  %-12s %-25s (%s)\n", b.Key, b.Desc, b.Context)
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Run bv --help for all options.")
	return nil
}

func registerPhaseOneRobotHandlers(registry *RobotRegistry, cfg phaseOneRobotHandlerConfig) {
	if registry == nil {
		panic("robot registry must not be nil")
	}

	registry.Register(RobotCommand{
		Name:        "robot-help",
		FlagName:    "robot-help",
		FlagPtr:     cfg.RobotHelpFlag,
		Description: "Show AI agent help",
		Handler: func(ctx RobotContext) error {
			if err := writeRobotHelp(ctx.StdoutOrDefault()); err != nil {
				return fmt.Errorf("writing robot help: %w", err)
			}
			return nil
		},
	})
	registry.Register(RobotCommand{
		Name:        "version",
		FlagName:    "version",
		FlagPtr:     cfg.VersionFlag,
		Description: "Show version",
		Handler: func(ctx RobotContext) error {
			_, err := fmt.Fprintf(ctx.StdoutOrDefault(), "bv %s\n", version.Version)
			if err != nil {
				return fmt.Errorf("writing version output: %w", err)
			}
			return nil
		},
	})
	registry.Register(RobotCommand{
		Name:        "robot-recipes",
		FlagName:    "robot-recipes",
		FlagPtr:     cfg.RobotRecipesFlag,
		Description: "Output recipe summaries for AI agents",
		Handler: func(ctx RobotContext) error {
			var loader *recipe.Loader
			if cfg.RecipeLoader != nil {
				loader = cfg.RecipeLoader()
			}
			if loader == nil {
				return fmt.Errorf("recipe loader not initialized")
			}

			summaries := loader.ListSummaries()
			sort.Slice(summaries, func(i, j int) bool {
				return summaries[i].Name < summaries[j].Name
			})

			output := struct {
				Recipes []recipe.RecipeSummary `json:"recipes"`
			}{
				Recipes: summaries,
			}

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding recipes: %w", err)
			}
			return nil
		},
	})
	registry.Register(RobotCommand{
		Name:        "robot-schema",
		FlagName:    "robot-schema",
		FlagPtr:     cfg.RobotSchemaFlag,
		Description: "Output JSON schema definitions for robot commands",
		Handler: func(ctx RobotContext) error {
			schemas := generateRobotSchemas()

			if cfg.SchemaCommand != nil && strings.TrimSpace(*cfg.SchemaCommand) != "" {
				commandName := strings.TrimSpace(*cfg.SchemaCommand)
				if schema, ok := schemas.Commands[commandName]; ok {
					singleOutput := map[string]interface{}{
						"schema_version": schemas.SchemaVersion,
						"generated_at":   schemas.GeneratedAt,
						"command":        commandName,
						"schema":         schema,
					}
					if err := ctx.EncoderOrDefault().Encode(singleOutput); err != nil {
						return fmt.Errorf("encoding schema: %w", err)
					}
					return nil
				}

				fmt.Fprintf(ctx.StderrOrDefault(), "Unknown command: %s\n", commandName)
				fmt.Fprintln(ctx.StderrOrDefault(), "Available commands:")
				commandNames := make([]string, 0, len(schemas.Commands))
				for name := range schemas.Commands {
					commandNames = append(commandNames, name)
				}
				sort.Strings(commandNames)
				for _, name := range commandNames {
					fmt.Fprintf(ctx.StderrOrDefault(), "  %s\n", name)
				}
				return newReportedRobotHandlerExit(1)
			}

			if err := ctx.EncoderOrDefault().Encode(schemas); err != nil {
				return fmt.Errorf("encoding schemas: %w", err)
			}
			return nil
		},
	})
	registry.Register(RobotCommand{
		Name:        "robot-metrics",
		FlagName:    "robot-metrics",
		FlagPtr:     cfg.RobotMetricsFlag,
		Description: "Output runtime performance metrics",
		Handler: func(ctx RobotContext) error {
			if err := ctx.EncoderOrDefault().Encode(metrics.GetAllMetrics()); err != nil {
				return fmt.Errorf("encoding metrics: %w", err)
			}
			return nil
		},
	})
}

func registerPhaseTwoRobotHandlers(registry *RobotRegistry, cfg phaseTwoRobotHandlerConfig) {
	if registry == nil {
		panic("robot registry must not be nil")
	}

	registry.Register(RobotCommand{
		Name:        "robot-plan",
		FlagName:    "robot-plan",
		FlagPtr:     cfg.RobotPlanFlag,
		Description: "Output dependency-respecting execution plan",
		Handler: func(ctx RobotContext) error {
			analyzer := analysis.NewAnalyzer(ctx.Issues)
			config := analysis.ConfigForSize(len(ctx.Issues), countEdges(ctx.Issues))
			if cfg.ForceFullAnalysis != nil && *cfg.ForceFullAnalysis {
				config = analysis.FullAnalysisConfig()
			} else {
				const skipReason = "not computed for --robot-plan"
				config.ComputePageRank = false
				config.PageRankSkipReason = skipReason
				config.ComputeBetweenness = false
				config.BetweennessMode = analysis.BetweennessSkip
				config.BetweennessSkipReason = skipReason
				config.ComputeHITS = false
				config.HITSSkipReason = skipReason
				config.ComputeEigenvector = false
				config.ComputeCriticalPath = false
				config.ComputeCycles = false
				config.CyclesSkipReason = skipReason
			}

			plan := analyzer.GetExecutionPlan()
			stats := analyzer.AnalyzeAsyncWithConfig(context.Background(), config)
			stats.WaitForPhase2()

			output := struct {
				GeneratedAt    string                  `json:"generated_at"`
				DataHash       string                  `json:"data_hash"`
				AsOf           string                  `json:"as_of,omitempty"`
				AsOfCommit     string                  `json:"as_of_commit,omitempty"`
				AnalysisConfig analysis.AnalysisConfig `json:"analysis_config"`
				Status         analysis.MetricStatus   `json:"status"`
				LabelScope     string                  `json:"label_scope,omitempty"`
				LabelContext   *analysis.LabelHealth   `json:"label_context,omitempty"`
				Plan           analysis.ExecutionPlan  `json:"plan"`
				UsageHints     []string                `json:"usage_hints"`
			}{
				GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
				DataHash:       ctx.DataHash,
				AsOf:           ctx.AsOf,
				AsOfCommit:     ctx.AsOfCommit,
				AnalysisConfig: config,
				Status:         stats.Status(),
				LabelScope:     ctx.LabelScope,
				LabelContext:   ctx.LabelContext,
				Plan:           plan,
				UsageHints: []string{
					"jq '.plan.tracks | length' - Number of parallel execution tracks",
					"jq '.plan.tracks[0].items | map(.id)' - First track item IDs",
					"jq '.plan.tracks[].items[] | select(.unblocks | length > 0)' - Items that unblock others",
					"jq '.plan.summary' - High-level execution summary",
					"jq '[.plan.tracks[].items[]] | length' - Total items across all tracks",
				},
			}

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding execution plan: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-priority",
		FlagName:    "robot-priority",
		FlagPtr:     cfg.RobotPriorityFlag,
		Description: "Output enhanced priority recommendations",
		Handler: func(ctx RobotContext) error {
			analyzer := analysis.NewAnalyzer(ctx.Issues)
			config := analysis.ConfigForSize(len(ctx.Issues), countEdges(ctx.Issues))
			if cfg.ForceFullAnalysis != nil && *cfg.ForceFullAnalysis {
				config = analysis.FullAnalysisConfig()
			}
			analyzer.SetConfig(&config)
			stats := analyzer.AnalyzeAsyncWithConfig(context.Background(), config)
			stats.WaitForPhase2()

			recommendations := analyzer.GenerateEnhancedRecommendations()
			filtered := make([]analysis.EnhancedPriorityRecommendation, 0, len(recommendations))
			issueMap := make(map[string]model.Issue, len(ctx.Issues))
			for _, issue := range ctx.Issues {
				issueMap[issue.ID] = issue
			}
			for _, rec := range recommendations {
				if cfg.RobotMinConf != nil && *cfg.RobotMinConf > 0 && rec.Confidence < *cfg.RobotMinConf {
					continue
				}
				if cfg.RobotByLabel != nil && strings.TrimSpace(*cfg.RobotByLabel) != "" {
					issue, ok := issueMap[rec.IssueID]
					if !ok {
						continue
					}
					hasLabel := false
					for _, label := range issue.Labels {
						if label == *cfg.RobotByLabel {
							hasLabel = true
							break
						}
					}
					if !hasLabel {
						continue
					}
				}
				if cfg.RobotByAssignee != nil && strings.TrimSpace(*cfg.RobotByAssignee) != "" {
					issue, ok := issueMap[rec.IssueID]
					if !ok || issue.Assignee != *cfg.RobotByAssignee {
						continue
					}
				}
				filtered = append(filtered, rec)
			}
			recommendations = filtered

			maxResults := 10
			if cfg.RobotMaxResults != nil && *cfg.RobotMaxResults > 0 {
				maxResults = *cfg.RobotMaxResults
			}
			if len(recommendations) > maxResults {
				recommendations = recommendations[:maxResults]
			}

			highConfidence := 0
			for _, rec := range recommendations {
				if rec.Confidence >= 0.7 {
					highConfidence++
				}
			}

			output := struct {
				GeneratedAt       string                                    `json:"generated_at"`
				DataHash          string                                    `json:"data_hash"`
				AsOf              string                                    `json:"as_of,omitempty"`
				AsOfCommit        string                                    `json:"as_of_commit,omitempty"`
				AnalysisConfig    analysis.AnalysisConfig                   `json:"analysis_config"`
				Status            analysis.MetricStatus                     `json:"status"`
				LabelScope        string                                    `json:"label_scope,omitempty"`
				LabelContext      *analysis.LabelHealth                     `json:"label_context,omitempty"`
				Recommendations   []analysis.EnhancedPriorityRecommendation `json:"recommendations"`
				FieldDescriptions map[string]string                         `json:"field_descriptions"`
				Filters           struct {
					MinConfidence float64 `json:"min_confidence,omitempty"`
					MaxResults    int     `json:"max_results"`
					ByLabel       string  `json:"by_label,omitempty"`
					ByAssignee    string  `json:"by_assignee,omitempty"`
				} `json:"filters"`
				Summary struct {
					TotalIssues     int `json:"total_issues"`
					Recommendations int `json:"recommendations"`
					HighConfidence  int `json:"high_confidence"`
				} `json:"summary"`
				Usage []string `json:"usage_hints"`
			}{
				GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
				DataHash:          ctx.DataHash,
				AsOf:              ctx.AsOf,
				AsOfCommit:        ctx.AsOfCommit,
				AnalysisConfig:    config,
				Status:            stats.Status(),
				LabelScope:        ctx.LabelScope,
				LabelContext:      ctx.LabelContext,
				Recommendations:   recommendations,
				FieldDescriptions: analysis.DefaultFieldDescriptions(),
				Usage: []string{
					"jq '.recommendations[] | select(.confidence > 0.7)' - Filter high confidence",
					"jq '.recommendations[] | {id: .issue_id, score: .impact_score, prio: .suggested_priority}' - Extract essentials",
					"jq '.summary' - Overview counts",
				},
			}
			if cfg.RobotMinConf != nil && *cfg.RobotMinConf > 0 {
				output.Filters.MinConfidence = *cfg.RobotMinConf
			}
			if cfg.RobotByLabel != nil && strings.TrimSpace(*cfg.RobotByLabel) != "" {
				output.Filters.ByLabel = *cfg.RobotByLabel
			}
			if cfg.RobotByAssignee != nil && strings.TrimSpace(*cfg.RobotByAssignee) != "" {
				output.Filters.ByAssignee = *cfg.RobotByAssignee
			}
			output.Filters.MaxResults = maxResults
			output.Summary.TotalIssues = len(ctx.Issues)
			output.Summary.Recommendations = len(recommendations)
			output.Summary.HighConfidence = highConfidence

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding priority recommendations: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-graph",
		FlagName:    "robot-graph",
		FlagPtr:     cfg.RobotGraphFlag,
		Description: "Output dependency graph in JSON, DOT, or Mermaid",
		Handler: func(ctx RobotContext) error {
			analyzer := analysis.NewAnalyzer(ctx.Issues)
			stats := analyzer.Analyze()

			format := export.GraphFormatJSON
			if cfg.GraphFormat != nil {
				switch strings.ToLower(strings.TrimSpace(*cfg.GraphFormat)) {
				case "dot":
					format = export.GraphFormatDOT
				case "mermaid":
					format = export.GraphFormatMermaid
				}
			}

			config := export.GraphExportConfig{
				Format:   format,
				Label:    ctx.LabelScope,
				DataHash: ctx.DataHash,
			}
			if cfg.GraphRoot != nil {
				config.Root = *cfg.GraphRoot
			}
			if cfg.GraphDepth != nil {
				config.Depth = *cfg.GraphDepth
			}

			result, err := export.ExportGraph(ctx.Issues, &stats, config)
			if err != nil {
				return fmt.Errorf("exporting graph: %w", err)
			}
			if err := ctx.EncoderOrDefault().Encode(result); err != nil {
				return fmt.Errorf("encoding graph: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-alerts",
		FlagName:    "robot-alerts",
		FlagPtr:     cfg.RobotAlertsFlag,
		Description: "Output drift and proactive alerts",
		Handler: func(ctx RobotContext) error {
			projectDir, err := ctx.ProjectDirOrDefault()
			if err != nil {
				return fmt.Errorf("getting project directory: %w", err)
			}
			baselinePath, err := ctx.BaselinePathOrDefault()
			if err != nil {
				return fmt.Errorf("getting baseline path: %w", err)
			}

			driftConfig, err := drift.LoadConfig(projectDir)
			if err != nil {
				return fmt.Errorf("loading drift config: %w", err)
			}

			analyzer := analysis.NewAnalyzer(ctx.Issues)
			stats := analyzer.Analyze()

			openCount, closedCount, blockedCount := 0, 0, 0
			for _, issue := range ctx.Issues {
				switch issue.Status {
				case model.StatusClosed:
					closedCount++
				case model.StatusBlocked:
					blockedCount++
				case model.StatusOpen, model.StatusInProgress:
					openCount++
				}
			}
			actionableCount := len(analyzer.GetActionableIssues())
			cycles := stats.Cycles()
			curStats := baseline.GraphStats{
				NodeCount:       stats.NodeCount,
				EdgeCount:       stats.EdgeCount,
				Density:         stats.Density,
				OpenCount:       openCount,
				ClosedCount:     closedCount,
				BlockedCount:    blockedCount,
				CycleCount:      len(cycles),
				ActionableCount: actionableCount,
			}

			bl := &baseline.Baseline{Stats: curStats}
			cur := &baseline.Baseline{Stats: curStats, Cycles: cycles}
			if baseline.Exists(baselinePath) {
				loaded, err := baseline.Load(baselinePath)
				if err != nil {
					if !ctx.EnvRobot {
						fmt.Fprintf(ctx.StderrOrDefault(), "Warning: Error loading baseline: %v\n", err)
					}
				} else {
					bl = loaded
					topMetrics := baseline.TopMetrics{
						PageRank:     buildMetricItems(stats.PageRank(), 10),
						Betweenness:  buildMetricItems(stats.Betweenness(), 10),
						CriticalPath: buildMetricItems(stats.CriticalPathScore(), 10),
						Hubs:         buildMetricItems(stats.Hubs(), 10),
						Authorities:  buildMetricItems(stats.Authorities(), 10),
					}
					cur = &baseline.Baseline{Stats: curStats, TopMetrics: topMetrics, Cycles: cycles}
				}
			}

			calc := drift.NewCalculator(bl, cur, driftConfig)
			calc.SetIssues(ctx.Issues)
			driftResult := calc.Calculate()

			filtered := driftResult.Alerts[:0]
			for _, alert := range driftResult.Alerts {
				if cfg.AlertSeverity != nil && strings.TrimSpace(*cfg.AlertSeverity) != "" && string(alert.Severity) != *cfg.AlertSeverity {
					continue
				}
				if cfg.AlertType != nil && strings.TrimSpace(*cfg.AlertType) != "" && string(alert.Type) != *cfg.AlertType {
					continue
				}
				if cfg.AlertLabel != nil && strings.TrimSpace(*cfg.AlertLabel) != "" {
					found := false
					for _, detail := range alert.Details {
						if strings.Contains(strings.ToLower(detail), strings.ToLower(*cfg.AlertLabel)) {
							found = true
							break
						}
					}
					if !found && alert.Label != "" && !strings.Contains(strings.ToLower(alert.Label), strings.ToLower(*cfg.AlertLabel)) {
						continue
					}
				}
				filtered = append(filtered, alert)
			}
			driftResult.Alerts = filtered

			output := struct {
				RobotEnvelope
				Alerts  []drift.Alert `json:"alerts"`
				Summary struct {
					Total    int `json:"total"`
					Critical int `json:"critical"`
					Warning  int `json:"warning"`
					Info     int `json:"info"`
				} `json:"summary"`
				UsageHints []string `json:"usage_hints"`
			}{
				RobotEnvelope: NewRobotEnvelope(ctx.DataHash),
				Alerts:        driftResult.Alerts,
				UsageHints: []string{
					"--severity=warning --alert-type=stale_issue   # stale warnings only",
					"--alert-type=blocking_cascade                 # high-unblock opportunities",
					"jq '.alerts | map(.issue_id)'                # list impacted issues",
				},
			}
			for _, alert := range driftResult.Alerts {
				switch alert.Severity {
				case drift.SeverityCritical:
					output.Summary.Critical++
				case drift.SeverityWarning:
					output.Summary.Warning++
				case drift.SeverityInfo:
					output.Summary.Info++
				}
				output.Summary.Total++
			}

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding alerts: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-suggest",
		FlagName:    "robot-suggest",
		FlagPtr:     cfg.RobotSuggestFlag,
		Description: "Output smart suggestions",
		Handler: func(ctx RobotContext) error {
			config := analysis.DefaultSuggestAllConfig()
			if cfg.SuggestConfidence != nil {
				config.MinConfidence = *cfg.SuggestConfidence
			}
			if cfg.SuggestBead != nil {
				config.FilterBead = *cfg.SuggestBead
			}

			suggestType := ""
			if cfg.SuggestType != nil {
				suggestType = strings.TrimSpace(*cfg.SuggestType)
			}
			switch suggestType {
			case "duplicate", "duplicates":
				config.FilterType = analysis.SuggestionPotentialDuplicate
			case "dependency", "dependencies":
				config.FilterType = analysis.SuggestionMissingDependency
			case "label", "labels":
				config.FilterType = analysis.SuggestionLabelSuggestion
			case "cycle", "cycles":
				config.FilterType = analysis.SuggestionCycleWarning
			case "":
			default:
				fmt.Fprintf(ctx.StderrOrDefault(), "Invalid suggest-type: %s (use: duplicate, dependency, label, cycle)\n", suggestType)
				return newReportedRobotHandlerExit(1)
			}

			output := analysis.GenerateRobotSuggestOutput(ctx.Issues, config, ctx.DataHash)
			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding suggestions: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-sprint-list",
		FlagName:    "robot-sprint-list",
		FlagPtr:     cfg.RobotSprintListFlag,
		Description: "Output all sprints as JSON",
		Handler: func(ctx RobotContext) error {
			workDir, err := ctx.WorkDirOrDefault()
			if err != nil {
				return fmt.Errorf("getting current directory: %w", err)
			}
			sprints, err := loader.LoadSprints(workDir)
			if err != nil {
				return fmt.Errorf("loading sprints: %w", err)
			}

			output := struct {
				RobotEnvelope
				SprintCount int            `json:"sprint_count"`
				Sprints     []model.Sprint `json:"sprints"`
			}{
				RobotEnvelope: NewRobotEnvelope(analysis.ComputeDataHash(ctx.Issues)),
				SprintCount:   len(sprints),
				Sprints:       sprints,
			}
			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding sprints: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-burndown",
		FlagName:    "robot-burndown",
		FlagPtr:     cfg.RobotBurndownFlag,
		Description: "Output sprint burndown as JSON",
		Handler: func(ctx RobotContext) error {
			workDir, err := ctx.WorkDirOrDefault()
			if err != nil {
				return fmt.Errorf("getting current directory: %w", err)
			}
			sprints, err := loader.LoadSprints(workDir)
			if err != nil {
				return fmt.Errorf("loading sprints: %w", err)
			}

			target := ""
			if cfg.RobotBurndownFlag != nil {
				target = *cfg.RobotBurndownFlag
			}

			var targetSprint *model.Sprint
			if target == "current" {
				for i := range sprints {
					if sprints[i].IsActive() {
						targetSprint = &sprints[i]
						break
					}
				}
				if targetSprint == nil {
					fmt.Fprintln(ctx.StderrOrDefault(), "No active sprint found")
					return newReportedRobotHandlerExit(1)
				}
			} else {
				for i := range sprints {
					if sprints[i].ID == target {
						targetSprint = &sprints[i]
						break
					}
				}
				if targetSprint == nil {
					fmt.Fprintf(ctx.StderrOrDefault(), "Sprint not found: %s\n", target)
					return newReportedRobotHandlerExit(1)
				}
			}

			now := time.Now()
			burndown := calculateBurndownAt(targetSprint, ctx.Issues, now)
			burndown.RobotEnvelope = NewRobotEnvelope(analysis.ComputeDataHash(ctx.Issues))
			issueMap := make(map[string]model.Issue, len(ctx.Issues))
			for _, issue := range ctx.Issues {
				issueMap[issue.ID] = issue
			}
			if scopeChanges, err := computeSprintScopeChanges(workDir, targetSprint, issueMap, now); err == nil && len(scopeChanges) > 0 {
				burndown.ScopeChanges = scopeChanges
			}

			if err := ctx.EncoderOrDefault().Encode(burndown); err != nil {
				return fmt.Errorf("encoding burndown: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-forecast",
		FlagName:    "robot-forecast",
		FlagPtr:     cfg.RobotForecastFlag,
		Description: "Output ETA forecasts as JSON",
		Handler: func(ctx RobotContext) error {
			workDir, err := ctx.WorkDirOrDefault()
			if err != nil {
				return fmt.Errorf("getting current directory: %w", err)
			}

			analyzer := analysis.NewAnalyzer(ctx.Issues)
			graphStats := analyzer.Analyze()

			targetIssues := make([]model.Issue, 0, len(ctx.Issues))
			var sprintBeadIDs map[string]bool
			if cfg.ForecastSprint != nil && strings.TrimSpace(*cfg.ForecastSprint) != "" {
				sprints, err := loader.LoadSprints(workDir)
				if err == nil {
					for _, sprint := range sprints {
						if sprint.ID == *cfg.ForecastSprint {
							sprintBeadIDs = make(map[string]bool)
							for _, beadID := range sprint.BeadIDs {
								sprintBeadIDs[beadID] = true
							}
							break
						}
					}
				}
				if sprintBeadIDs == nil {
					fmt.Fprintf(ctx.StderrOrDefault(), "Sprint not found: %s\n", *cfg.ForecastSprint)
					return newReportedRobotHandlerExit(1)
				}
			}

			for _, issue := range ctx.Issues {
				if cfg.ForecastLabel != nil && strings.TrimSpace(*cfg.ForecastLabel) != "" {
					hasLabel := false
					for _, label := range issue.Labels {
						if label == *cfg.ForecastLabel {
							hasLabel = true
							break
						}
					}
					if !hasLabel {
						continue
					}
				}
				if sprintBeadIDs != nil && !sprintBeadIDs[issue.ID] {
					continue
				}
				targetIssues = append(targetIssues, issue)
			}

			now := time.Now()
			agents := 1
			if cfg.ForecastAgents != nil && *cfg.ForecastAgents > 0 {
				agents = *cfg.ForecastAgents
			}

			type ForecastSummary struct {
				TotalMinutes  int       `json:"total_minutes"`
				TotalDays     float64   `json:"total_days"`
				AvgConfidence float64   `json:"avg_confidence"`
				EarliestETA   time.Time `json:"earliest_eta"`
				LatestETA     time.Time `json:"latest_eta"`
			}
			type ForecastOutput struct {
				RobotEnvelope
				Agents        int                    `json:"agents"`
				Filters       map[string]string      `json:"filters,omitempty"`
				ForecastCount int                    `json:"forecast_count"`
				Forecasts     []analysis.ETAEstimate `json:"forecasts"`
				Summary       *ForecastSummary       `json:"summary,omitempty"`
			}

			forecastTarget := ""
			if cfg.RobotForecastFlag != nil {
				forecastTarget = *cfg.RobotForecastFlag
			}

			forecasts := make([]analysis.ETAEstimate, 0)
			if forecastTarget == "all" {
				for _, issue := range targetIssues {
					if issue.Status == model.StatusClosed {
						continue
					}
					eta, err := analysis.EstimateETAForIssue(ctx.Issues, &graphStats, issue.ID, agents, now)
					if err != nil {
						continue
					}
					forecasts = append(forecasts, eta)
				}
			} else {
				eta, err := analysis.EstimateETAForIssue(ctx.Issues, &graphStats, forecastTarget, agents, now)
				if err != nil {
					fmt.Fprintf(ctx.StderrOrDefault(), "Error: %v\n", err)
					return newReportedRobotHandlerExit(1)
				}
				forecasts = append(forecasts, eta)
			}

			var summary *ForecastSummary
			if len(forecasts) > 1 {
				totalMinutes := 0
				totalConfidence := 0.0
				earliest := forecasts[0].ETADate
				latest := forecasts[0].ETADate
				for _, forecast := range forecasts {
					totalMinutes += forecast.EstimatedMinutes
					totalConfidence += forecast.Confidence
					if forecast.ETADate.Before(earliest) {
						earliest = forecast.ETADate
					}
					if forecast.ETADate.After(latest) {
						latest = forecast.ETADate
					}
				}
				summary = &ForecastSummary{
					TotalMinutes:  totalMinutes,
					TotalDays:     float64(totalMinutes) / (60.0 * 8.0),
					AvgConfidence: totalConfidence / float64(len(forecasts)),
					EarliestETA:   earliest,
					LatestETA:     latest,
				}
			}

			filters := make(map[string]string)
			if cfg.ForecastLabel != nil && strings.TrimSpace(*cfg.ForecastLabel) != "" {
				filters["label"] = *cfg.ForecastLabel
			}
			if cfg.ForecastSprint != nil && strings.TrimSpace(*cfg.ForecastSprint) != "" {
				filters["sprint"] = *cfg.ForecastSprint
			}

			output := ForecastOutput{
				RobotEnvelope: NewRobotEnvelope(analysis.ComputeDataHash(ctx.Issues)),
				Agents:        agents,
				ForecastCount: len(forecasts),
				Forecasts:     forecasts,
				Summary:       summary,
			}
			if len(filters) > 0 {
				output.Filters = filters
			}

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding forecast: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-search",
		FlagName:    "robot-search",
		FlagPtr:     cfg.RobotSearchFlag,
		Description: "Output semantic search results as JSON",
		Handler: func(ctx RobotContext) error {
			if ctx.SearchOutput == nil {
				return fmt.Errorf("robot search output not initialized")
			}
			if err := writeRobotSearchOutput(ctx.StdoutOrDefault(), *ctx.SearchOutput); err != nil {
				return fmt.Errorf("encoding robot-search: %w", err)
			}
			return nil
		},
	})

	registry.Register(RobotCommand{
		Name:        "robot-diff",
		FlagName:    "robot-diff",
		FlagPtr:     cfg.RobotDiffFlag,
		Description: "Output snapshot diff as JSON",
		Handler: func(ctx RobotContext) error {
			if ctx.Diff == nil {
				return fmt.Errorf("diff output not initialized")
			}
			output := struct {
				GeneratedAt      string                 `json:"generated_at"`
				ResolvedRevision string                 `json:"resolved_revision"`
				AsOf             string                 `json:"as_of,omitempty"`
				AsOfCommit       string                 `json:"as_of_commit,omitempty"`
				FromDataHash     string                 `json:"from_data_hash"`
				ToDataHash       string                 `json:"to_data_hash"`
				Diff             *analysis.SnapshotDiff `json:"diff"`
			}{
				GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
				ResolvedRevision: ctx.DiffResolvedRevision,
				AsOf:             ctx.AsOf,
				AsOfCommit:       ctx.AsOfCommit,
				FromDataHash:     analysis.ComputeDataHash(ctx.DiffHistoricalIssues),
				ToDataHash:       ctx.DataHash,
				Diff:             ctx.Diff,
			}

			if err := ctx.EncoderOrDefault().Encode(output); err != nil {
				return fmt.Errorf("encoding diff: %w", err)
			}
			return nil
		},
	})
}

func (r *RobotRegistry) Validate() error {
	registered := make(map[string]struct{}, len(r.commands))
	active := make(map[string]struct{}, len(r.commands))

	for _, cmd := range r.commands {
		registered[cmd.FlagName] = struct{}{}
		if robotFlagActive(cmd.FlagPtr) {
			active[cmd.FlagName] = struct{}{}
		}
	}

	for _, cmd := range r.commands {
		for _, coFlag := range cmd.RequiredCoFlags {
			if _, ok := registered[coFlag]; !ok {
				return fmt.Errorf("%s requires unregistered co-flag %s", formatRobotFlag(cmd.FlagName), formatRobotFlag(coFlag))
			}
		}
		if _, ok := active[cmd.FlagName]; !ok || len(cmd.RequiredCoFlags) == 0 {
			continue
		}
		if hasAnyRobotFlag(active, cmd.RequiredCoFlags) {
			continue
		}

		if len(cmd.RequiredCoFlags) == 1 {
			return fmt.Errorf("%s requires %s", formatRobotFlag(cmd.FlagName), formatRobotFlag(cmd.RequiredCoFlags[0]))
		}
		return fmt.Errorf("%s requires one of %s", formatRobotFlag(cmd.FlagName), joinRobotFlags(cmd.RequiredCoFlags))
	}

	return nil
}

func normalizeRobotFlagName(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), "--")
}

func normalizeRobotFlagNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		trimmed := normalizeRobotFlagName(name)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func formatRobotFlag(name string) string {
	normalized := normalizeRobotFlagName(name)
	if normalized == "" {
		return "--"
	}
	return "--" + normalized
}

func joinRobotFlags(names []string) string {
	flags := make([]string, 0, len(names))
	for _, name := range names {
		flags = append(flags, formatRobotFlag(name))
	}
	return strings.Join(flags, ", ")
}

func hasAnyRobotFlag(active map[string]struct{}, names []string) bool {
	for _, name := range names {
		if _, ok := active[normalizeRobotFlagName(name)]; ok {
			return true
		}
	}
	return false
}

func robotFlagActive(flagPtr interface{}) bool {
	if flagPtr == nil {
		return false
	}

	value := reflect.ValueOf(flagPtr)
	if !value.IsValid() {
		return false
	}

	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		return robotValueActive(value.Elem())
	}

	return robotValueActive(value)
}

func robotValueActive(value reflect.Value) bool {
	if !value.IsValid() {
		return false
	}

	switch value.Kind() {
	case reflect.Bool:
		return value.Bool()
	case reflect.String:
		return strings.TrimSpace(value.String()) != ""
	case reflect.Slice, reflect.Array, reflect.Map:
		return value.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return value.Float() != 0
	case reflect.Interface:
		if value.IsNil() {
			return false
		}
		return robotFlagActive(value.Interface())
	default:
		zero := reflect.Zero(value.Type()).Interface()
		return !reflect.DeepEqual(value.Interface(), zero)
	}
}
