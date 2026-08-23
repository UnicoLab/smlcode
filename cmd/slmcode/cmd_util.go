package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/agents"
	"github.com/UnicoLab/slmcode/pkg/backends"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
	"github.com/UnicoLab/slmcode/pkg/evolve"
	"github.com/UnicoLab/slmcode/pkg/harness"
	"github.com/UnicoLab/slmcode/pkg/models"
	"github.com/UnicoLab/slmcode/pkg/plan"
	"github.com/UnicoLab/slmcode/pkg/readiness"
	"github.com/UnicoLab/slmcode/pkg/retrieval"
	"github.com/UnicoLab/slmcode/pkg/schema"
	"github.com/UnicoLab/slmcode/pkg/skills"
)

func skillsCmd() *cobra.Command {
	listFn := func(cmd *cobra.Command, args []string) error {
		ws, err := openWorkspace()
		if err != nil {
			return err
		}
		list, err := ws.Skills.List()
		if err != nil {
			return err
		}
		cli.Header("Skills")
		if len(list) == 0 {
			fmt.Println(cli.Dim("  (none — add .slmcode/skills/<name>/SKILL.md)"))
			return nil
		}
		for _, s := range list {
			agents := "*"
			if len(s.Agents) > 0 {
				agents = strings.Join(s.Agents, ",")
			}
			fmt.Printf("  %s  %-10s  %s\n",
				cli.Accent(fmt.Sprintf("%-24s", s.Name)),
				cli.Dim(agents),
				cli.Dim(s.Description))
		}
		fmt.Println()
		fmt.Println(cli.Dim("  Reference in queries: @skill:name   or   /skill name"))
		fmt.Println(cli.Dim("  Project skills:       .slmcode/skills/<name>/SKILL.md"))
		return nil
	}

	cmd := &cobra.Command{
		Use:     "skills",
		Short:   "List / show / create / edit skills (Claude Code–style)",
		Example: "  slmcode skills list\n  slmcode skills new my-skill\n  slmcode skills show atomic-coding",
		RunE:    listFn,
	}
	cmd.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List skills", RunE: listFn})

	cmd.AddCommand(&cobra.Command{
		Use:   "show [name]",
		Short: "Print a skill (frontmatter + body)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			sk, ok := ws.Skills.Get(args[0])
			if !ok {
				return fmt.Errorf("skill %q not found — try: slmcode skills list", args[0])
			}
			cli.Header("skill:" + sk.Name)
			cli.KeyVal("description", sk.Description)
			cli.KeyVal("agents", strings.Join(sk.Agents, ", "))
			cli.KeyVal("triggers", strings.Join(sk.Triggers, ", "))
			cli.KeyVal("path", sk.Path)
			fmt.Println()
			fmt.Println(sk.Body)
			return nil
		},
	})

	newCmd := &cobra.Command{
		Use:   "new [name]",
		Short: "Create .slmcode/skills/<name>/SKILL.md",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentsFlag, _ := cmd.Flags().GetString("agents")
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(ws.Config.SkillsDir(), 0o750); err != nil { // .slmcode/skills, owner-only
				return err
			}
			if _, ok := ws.Skills.Get(args[0]); ok {
				return fmt.Errorf("skill %q already exists — use: slmcode skills edit %s", args[0], args[0])
			}
			sk := skills.Template(args[0], agentsFlag)
			path, err := skills.WriteSkill(ws.Config.SkillsDir(), sk)
			if err != nil {
				return err
			}
			fmt.Println(cli.Success("created " + path))
			fmt.Println(cli.Dim("  Reference with @skill:" + sk.Name + " in run/chat queries"))
			return nil
		},
	}
	newCmd.Flags().String("agents", "", "comma-separated specialist ids (empty = all)")
	cmd.AddCommand(newCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "edit [name]",
		Short: "Open skill in $EDITOR (creates project copy if bundled-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			name := args[0]
			sk, ok := ws.Skills.Get(name)
			if !ok {
				return fmt.Errorf("skill %q not found", name)
			}
			projPath := filepath.Join(ws.Config.SkillsDir(), sanitizeSkillName(name), "SKILL.md")
			if _, err := os.Stat(projPath); err != nil {
				sk.Name = name
				if _, err := skills.WriteSkill(ws.Config.SkillsDir(), sk); err != nil {
					return err
				}
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			// editor is from $EDITOR (or the "vi" default), which the invoking
			// user controls on their own machine.
			c := exec.Command(editor, projPath) //nolint:gosec // editor path is from the user's own env, not attacker input
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "path [name]",
		Short: "Print skill file path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			sk, ok := ws.Skills.Get(args[0])
			if !ok {
				return fmt.Errorf("skill %q not found", args[0])
			}
			fmt.Println(sk.Path)
			return nil
		},
	})

	return cmd
}

func sanitizeSkillName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "skill"
	}
	return b.String()
}

func doctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "doctor",
		Short:   "Check active provider/model, LLM reachability, workspace, board, skills",
		Example: "  slmcode doctor\n  slmcode doctor --json",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			if asJSON {
				return runDoctorJSON()
			}
			return runDoctor()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

// runDoctorJSON emits the same health picture as runDoctor, machine-readable.
func runDoctorJSON() error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	skillList, _ := ws.Skills.List()
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	providerCheck := readiness.ProbeProvider(probeCtx, ws.Config)
	probeCancel()
	report := readiness.Build(ws.Config, len(skillList))
	report.Checks = append(report.Checks, providerCheck)
	report.Score = readiness.Score(report.Checks)
	report.Status = readiness.Status(report.Score)
	report.OK = report.Score >= 80
	_ = ws.Board.Load()
	board := ws.Board.Snapshot()
	auth := models.ResolveAuth(ws.Config)

	decoding := models.DescribeDecoding(ws.Config)
	payload := map[string]any{
		"root":       ws.Config.Root,
		"provider":   ws.Config.Provider,
		"model":      ws.Config.Model,
		"endpoint":   ws.Config.Endpoint,
		"backend":    ws.Config.Backend,
		"permission": ws.Config.Permission,
		"skills":     len(skillList),
		"decoding": map[string]any{
			"policy":    ws.Config.StructuredDecoding,
			"mechanism": decoding.Mechanism,
			"source":    decoding.Source,
			"probed":    decoding.Probed,
			"summary":   decoding.Summary(),
			"support":   decoding,
			"cached":    backends.CapabilityReport(),
			"grammars":  grammarSizes(),
		},
		"throughput": map[string]any{
			"model":    throughputForModel(ws.Config.Model),
			"observed": backends.ThroughputSnapshot(),
		},
		"learning": learningStatus(ws.Config),
		"config": map[string]any{
			"path":           ws.Config.ConfigPath(),
			"user_path":      ws.Config.Provenance().UserPath,
			"config_version": config.CurrentConfigVersion,
			"explicit_keys":  ws.Config.Diff(),
			"warnings":       ws.Config.Provenance().Warnings,
		},
		"tasks":   len(board.Tasks),
		"pending": pendingCount(ws.Config.SlmDir()),
		"auth": map[string]any{
			"configured": auth.Configured,
			"required":   auth.Required,
			"source":     auth.Source,
		},
		"gitignore": gitignoreStatus(ws.Config.Root, ws.Config.SlmDir()),
		"readiness": report,
	}
	if err := emitJSON(payload); err != nil {
		return err
	}
	if !providerCheck.OK && providerCheck.Severity == "critical" {
		return failf(4, "provider check failed: %s", providerCheck.Message)
	}
	return nil
}

// gitignoreStatus reports whether the secret-bearing .slmcode paths are ignored.
//
// The probe set comes from pkg/config, the same list `slmcode init` renders
// into `.slmcode/.gitignore`, so doctor can never check fewer paths than init
// writes. Directory rules end in "/" so they only match directories; probing
// them with a representative child path makes the answer correct whether or
// not the directory exists yet.
func gitignoreProbes() map[string]string { return config.SlmIgnoreProbes() }

func gitignoreStatus(root, slmDir string) map[string]any {
	out := map[string]any{}
	ok := true
	for name, probe := range gitignoreProbes() {
		ignored := gitIgnores(root, probe)
		out[name] = ignored
		if !ignored {
			ok = false
		}
	}
	out["ok"] = ok
	out["file"] = filepath.Join(slmDir, ".gitignore")
	return out
}

// gitignoreGaps names every .slmcode path git would currently stage, in the
// order pkg/config lists them (credentials first, then run content).
func gitignoreGaps(status map[string]any) []string {
	var leaky []string
	for _, e := range config.SlmIgnoreEntries {
		name := strings.TrimSuffix(e.Pattern, "/")
		if ignored, ok := status[name].(bool); ok && !ignored {
			leaky = append(leaky, ".slmcode/"+e.Pattern)
		}
	}
	return leaky
}

func runDoctor() error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	cli.Header("Doctor")
	cli.KeyVal("root", ws.Config.Root)
	cli.KeyVal("provider", ws.Config.Provider)
	cli.KeyVal("model", ws.Config.Model)
	cli.KeyVal("endpoint", ws.Config.Endpoint)
	if ws.Config.ActiveStack != "" {
		cli.KeyVal("active_stack", ws.Config.ActiveStack)
	} else {
		cli.KeyVal("active_stack", "(none — manual provider/model)")
	}
	cli.KeyVal("backend", ws.Config.Backend)
	cli.KeyVal("permission", ws.Config.Permission)
	cli.KeyVal("shell", ws.Config.ShellPermission)
	cli.KeyVal("dynamic_pipeline", fmt.Sprintf("%v", ws.Config.DynamicPipeline))
	cli.KeyVal("mode", ws.Config.Mode)
	cli.KeyVal("specialist", ws.Config.Specialist)
	cli.KeyVal("qa_gate", fmt.Sprintf("%v (rounds=%d)", ws.Config.QAGate, ws.Config.QAGateMaxRounds))
	prof := config.ResolveModelProfile(ws.Config.ModelProfiles, ws.Config.Model)
	cli.KeyVal("model_profile", fmt.Sprintf("ctx=%d max_tokens=%d think=%d skills=%d knowledge=%d turns=%d",
		prof.ContextLimit, prof.MaxTokens, prof.ThinkingBudgetTokens,
		prof.SkillTokenBudget, prof.KnowledgeTokenBudget, prof.MaxTurns))
	cli.KeyVal("guards", fmt.Sprintf("write=%v shell_write=%v read_before_edit=%v smoke=%v claims=%v over_edit=%v",
		ws.Config.WriteGuard, ws.Config.ShellWriteGuard, ws.Config.ReadBeforeEdit,
		ws.Config.RequireSmoke, ws.Config.ClaimsGate, ws.Config.OverEditGuard))
	embCfg := retrieval.Config{
		Enabled:  ws.Config.EmbeddingEnabled,
		Endpoint: ws.Config.EmbeddingEndpoint,
		Model:    ws.Config.EmbeddingModel,
		APIKey:   ws.Config.EmbeddingAPIKey,
		TopK:     ws.Config.EmbeddingTopK,
	}
	_, embMode := retrieval.ResolveEmbedder(context.Background(), embCfg)
	cli.KeyVal("embedding", fmt.Sprintf("%s enabled=%v model=%s endpoint=%s top_k=%d",
		embMode, ws.Config.EmbeddingEnabled, ws.Config.EmbeddingModel, ws.Config.EmbeddingEndpoint, ws.Config.EmbeddingTopK))
	// Which decoding mechanism a strict contract actually gets. Invisible
	// until output quality drops, so doctor is the place to say it out loud.
	decoding := models.DescribeDecoding(ws.Config)
	cli.KeyVal("decoding", fmt.Sprintf("%s (policy=%s)", decoding.Summary(), ws.Config.StructuredDecoding))
	switch {
	case config.NormalizeStructuredDecoding(ws.Config.StructuredDecoding) == config.DecodingOff:
		// Under `off` the orchestrator pins prompt-only capabilities at
		// construction, so no probe will ever happen. Saying "the first run
		// probes the endpoint" here would describe the opposite of the truth.
		fmt.Println(cli.Dim("  constrained decoding is OFF — every role uses prompt-only JSON; " +
			"no capability probe is issued"))
	case !decoding.Probed:
		fmt.Println(cli.Dim("  not negotiated yet — the first run probes the endpoint"))
	}
	for _, line := range backends.CapabilityReport() {
		fmt.Println(cli.Dim("  " + line))
	}
	// Constrained decoding is only as strong as the contract behind it, so
	// say how many role grammars actually render and how big they are. A role
	// with no schema contract silently degrades to prompt-only JSON.
	cli.KeyVal("grammars", describeGrammars())
	// Measured decode rate. EstimateTimeout already derives request deadlines
	// from this; without it here an operator can only infer throughput from
	// how long a run felt.
	cli.KeyVal("throughput", describeThroughput(ws.Config.Model))
	for _, line := range throughputLines() {
		fmt.Println(cli.Dim("  " + line))
	}
	learn := learningStatus(ws.Config)
	cli.KeyVal("evolve", fmt.Sprintf("%v (%s)", learn["evolve"], learn["evolve_detail"]))
	cli.KeyVal("memory", fmt.Sprintf("%v (%s)", learn["memory"], learn["memory_detail"]))
	cli.KeyVal("config", fmt.Sprintf("%s — %d explicit key(s)",
		ws.Config.ConfigPath(), len(ws.Config.Diff())))
	if p := ws.Config.Provenance().UserPath; p != "" {
		cli.KeyVal("user config", p)
	}
	for _, w := range ws.Config.Provenance().Warnings {
		fmt.Println(cli.Warn(w))
	}
	// harness.Initialized, not a stat of .slmcode/: several commands mkdir the
	// directory as a side effect, so its existence proved nothing and doctor
	// happily reported "✔ .slmcode present" in a project that had never been
	// initialized. config.yaml is the marker.
	initialized := harness.Initialized(ws.Config.Root)
	if initialized {
		fmt.Println(cli.Success(".slmcode workspace initialized"))
	} else {
		fmt.Println(cli.Warn("no workspace here — everything below is a built-in default"))
		fmt.Println(cli.Dim("  fix: slmcode init"))
	}
	_ = ws.Board.Load()
	b := ws.Board.Snapshot()
	if initialized {
		fmt.Println(cli.Success(fmt.Sprintf("board: %d tasks", len(b.Tasks))))
	}

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	providerCheck := readiness.ProbeProvider(probeCtx, ws.Config)
	probeCancel()
	printDoctorProviderProbe(providerCheck)
	auth := models.ResolveAuth(ws.Config)
	// A 401 from the probe means the key we have is the WRONG key. Printing
	// "✔ auth OK" one line under "✖ LLM check failed — HTTP 401" told the user
	// the exact opposite of what had just happened.
	if rejected := providerRejectedAuth(providerCheck); rejected {
		fmt.Println(cli.Error(fmt.Sprintf("auth rejected by the provider (key from %s)", auth.Source)))
		fmt.Println(cli.Dim("  fix: slmcode auth set " + ws.Config.Provider + "   or   export SLMCODE_API_KEY=…"))
	} else if auth.Configured {
		fmt.Println(cli.Success(fmt.Sprintf("auth OK (%s)", auth.Source)))
	} else if auth.Required {
		fmt.Println(cli.Error(auth.Message))
	} else {
		fmt.Println(cli.Dim("auth: local provider — key optional"))
	}
	custom, _ := agents.LoadCustomSpecs(append([]string{ws.Config.AgentsDir()}, agents.GlobalAgentRoots()...)...)
	var pinned []string
	for _, a := range custom {
		if a.Model != "" || a.Provider != "" || a.Endpoint != "" {
			pinned = append(pinned, a.ID)
		}
	}
	if len(pinned) > 0 {
		fmt.Println(cli.Warn(fmt.Sprintf("agents pinning LLM (override stack): %s", strings.Join(pinned, ", "))))
		fmt.Println(cli.Dim("  tip: slmcode stack apply <name> --clear-agent-llm   or   slmcode agent clear-llm <id>"))
	} else {
		fmt.Println(cli.Success("agents inherit stack/global LLM"))
	}
	// .slmcode/auth.json holds provider API keys; `slmcode commit` runs
	// `git add -A`, so an un-ignored .slmcode is a real leak path.
	if gs := gitignoreStatus(ws.Config.Root, ws.Config.SlmDir()); gs["ok"] != true {
		leaky := gitignoreGaps(gs)
		fmt.Println(cli.Warn(fmt.Sprintf("git would stage %d of %d .slmcode paths: %s",
			len(leaky), len(config.SlmIgnoreEntries), strings.Join(leaky, ", "))))
		fmt.Println(cli.Dim("  fix: delete .slmcode/.gitignore and re-run `slmcode init` (it rewrites the full list)"))
	} else {
		fmt.Println(cli.Success(".slmcode secrets are git-ignored"))
	}
	sk, _ := ws.Skills.List()
	fmt.Println(cli.Success(fmt.Sprintf("%d skills loaded", len(sk))))
	report := readiness.Build(ws.Config, len(sk))
	report.Checks = append(report.Checks, providerCheck)
	report.Score = readiness.Score(report.Checks)
	report.Status = readiness.Status(report.Score)
	report.OK = report.Score >= 80
	if readinessHasFailedChecks(report) {
		fmt.Println(cli.Warn(fmt.Sprintf("readiness: %d/100 (%s)", report.Score, report.Status)))
		fmt.Println(cli.Dim("  failed: " + strings.Join(readinessFailedIDs(report), ", ")))
		fmt.Println(cli.Dim("  fix: slmcode readiness --fix"))
	} else {
		fmt.Println(cli.Success(fmt.Sprintf("readiness: %d/100 (%s)", report.Score, report.Status)))
	}
	return nil
}

// learningStatus reports whether the self-improvement subsystems are live and
// how much they currently hold, so "is evolve on?" has an answer that does not
// require reading JSONL.
func learningStatus(cfg *config.Config) map[string]any {
	out := map[string]any{
		"evolve":        cfg.Evolve,
		"deterministic": !cfg.ExploreEnabled(),
		"memory":        cfg.Evolve && cfg.MemoryTokens > 0,
		"memory_tokens": cfg.MemoryTokens,
		"regressions":   cfg.RegressionChecks,
	}
	if !cfg.Evolve {
		out["evolve_detail"] = "disabled — no rules, policy or memory are updated"
		out["memory_detail"] = "disabled with evolve"
		return out
	}
	explore := "exploring"
	if !cfg.ExploreEnabled() {
		explore = "greedy (deterministic)"
	}
	home, _ := os.UserHomeDir()
	eng, err := evolve.OpenWith(cfg.Root, home, evolve.EngineOptions{ReadOnly: true})
	if err != nil && eng == nil {
		out["evolve_detail"] = explore + " · state unreadable: " + err.Error()
		out["memory_detail"] = "unreadable"
		return out
	}
	defer func() { _ = eng.Close() }()
	rules, bandit, regs := eng.Rules().Count(), len(eng.Bandit().Snapshot()), eng.Regressions().Count()
	out["rules"] = rules
	out["policy_keys"] = bandit
	out["regression_checks"] = regs
	out["evolve_detail"] = fmt.Sprintf("%s · %d rules · %d policy keys · %d regression checks",
		explore, rules, bandit, regs)
	if mem := eng.Memory(); mem != nil {
		eps, facts, procs := mem.Episodes().Count(), mem.Semantic().Count(), mem.Procedural().Count()
		out["episodes"], out["facts"], out["procedures"] = eps, facts, procs
		out["memory_detail"] = fmt.Sprintf("%d episodes · %d facts · %d procedures · %d token budget",
			eps, facts, procs, cfg.MemoryTokens)
	} else {
		out["memory_detail"] = "unavailable"
	}
	return out
}

func printDoctorProviderProbe(check readiness.Check) {
	fmt.Print(formatDoctorProviderProbe(check))
}

func formatDoctorProviderProbe(check readiness.Check) string {
	var b strings.Builder
	if check.OK {
		msg := check.Message
		if check.Latency > 0 {
			msg = fmt.Sprintf("%s · %d ms", msg, check.Latency)
		}
		b.WriteString(cli.Success("LLM ok — " + msg))
		b.WriteString("\n")
		return b.String()
	}
	if check.Severity == "critical" {
		b.WriteString(cli.Error("LLM check failed — " + check.Message))
	} else {
		b.WriteString(cli.Warn("LLM check warning — " + check.Message))
	}
	b.WriteString("\n")
	if check.Endpoint != "" {
		b.WriteString(cli.Dim("  endpoint: " + check.Endpoint))
		b.WriteString("\n")
	}
	// Prefer the specific remedy over the generic one. readiness returns the
	// same "confirm the endpoint is reachable" hint for every failure, which
	// is the wrong advice for a 401 (reachable, key rejected) and for a 404
	// (reachable, wrong path) — the two failures a new user actually hits.
	cause, remedy := doctorRemedy(check)
	switch {
	case remedy != "":
		if cause != "" {
			b.WriteString(cli.Dim("  cause: " + cause))
			b.WriteString("\n")
		}
		b.WriteString(cli.Dim("  tip: " + remedy))
		b.WriteString("\n")
	case check.FixHint != "":
		b.WriteString(cli.Dim("  tip: " + check.FixHint))
		b.WriteString("\n")
	default:
		b.WriteString(cli.Dim("  tip: start your provider, or override with --provider / --endpoint / --model"))
		b.WriteString("\n")
	}
	// The generic "fix" label only helps when there was no specific tip; after
	// a 401 remedy, "Check endpoint and start the model server" is noise that
	// contradicts the line above it.
	if check.FixLabel != "" && remedy == "" {
		b.WriteString(cli.Dim("  fix: " + check.FixLabel))
		b.WriteString("\n")
	}
	return b.String()
}

func readinessFailedIDs(r readiness.Report) []string {
	var out []string
	for _, check := range r.Checks {
		if !check.OK {
			out = append(out, check.ID)
		}
	}
	return out
}

func watchCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:     "watch",
		Short:   "Refresh kanban in the terminal (live while agents run)",
		Example: "  slmcode watch\n  slmcode watch --interval 5s",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			if interval <= 0 {
				interval = 2 * time.Second
			}
			// Use the alternate screen buffer so the repeated repaint never eats
			// the user's scrollback: on exit the original screen comes back.
			alt := cli.IsInteractive()
			if alt {
				fmt.Print("\033[?1049h")
				defer fmt.Print("\033[?1049l")
			}
			ctx, cancel := signalContext()
			defer cancel()
			fmt.Println(cli.Info("watching board — Ctrl+C to stop"))
			for {
				_ = ws.Board.Load()
				b := ws.Board.Snapshot()
				if alt {
					fmt.Print("\033[H\033[2J")
				}
				fmt.Print(cli.Banner())
				cli.KeyVal("updated", time.Now().Format(time.Kitchen))
				fmt.Println()
				by := b.ByColumn()
				for _, col := range plan.Columns() {
					tasks := by[col]
					fmt.Printf("%s %s\n", cli.ColumnColor("●"), cli.Bold(plan.ColumnLabel(col))+cli.Dim(fmt.Sprintf(" (%d)", len(tasks))))
					for _, t := range tasks {
						fmt.Printf("    %s  %s\n", cli.Accent(t.ID), t.Title)
					}
				}
				select {
				case <-time.After(interval):
				case <-ctx.Done():
					return nil
				case <-cmd.Context().Done():
					return nil
				}
			}
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "refresh interval")
	return cmd
}

// openBrowser tries to open url in the default browser.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Run()
	}
}

// ---------------------------------------------------------------------------
// doctor: schema grammars and measured throughput
// ---------------------------------------------------------------------------

// grammarSizes is the rendered GBNF size, in bytes, of every registered role
// contract. This is the diagnostic schema.AllGrammars exists for: an empty map
// (or a missing role) means constrained decoding has nothing to constrain.
func grammarSizes() map[string]int {
	out := map[string]int{}
	for role, src := range schema.AllGrammars() {
		out[role] = len(src)
	}
	return out
}

// describeGrammars summarizes the role contracts for the human doctor, naming
// the reviewer grammar explicitly because pkg/models negotiates the decoding
// mechanism against exactly that spec.
func describeGrammars() string {
	sizes := grammarSizes()
	total := 0
	for _, n := range sizes {
		total += n
	}
	line := fmt.Sprintf("%d role contract(s), %d GBNF bytes", len(sizes), total)
	if g, ok := schema.GBNFForRole(schema.RoleReview); ok {
		line += fmt.Sprintf(" · %s=%d B (negotiation spec)", schema.RoleReview, len(g))
	} else {
		line += " · no " + schema.RoleReview + " grammar — decoding falls back to prompt-only"
	}
	return line
}

// throughputForModel is the machine-readable measured decode rate, or nil when
// nothing has been measured for this model yet. Never substitutes
// backends.DefaultTokensPerSec: that prior sizes request deadlines, and
// reporting it here would present a guess as an observation.
func throughputForModel(model string) map[string]any {
	tps, samples, ok := backends.ObservedThroughput(model)
	if !ok {
		return nil
	}
	return map[string]any{"tokens_per_sec": tps, "samples": samples}
}

// describeThroughput renders the active model's measured decode rate.
func describeThroughput(model string) string {
	tps, samples, ok := backends.ObservedThroughput(model)
	if !ok {
		return "not measured yet — run something, then re-check"
	}
	return fmt.Sprintf("≈%.1f tok/s over %d completion(s)", tps, samples)
}

// throughputLines lists every model observed on this machine, so a stack swap
// can be compared against the model it replaced.
func throughputLines() []string {
	snap := backends.ThroughputSnapshot()
	if len(snap) == 0 {
		return nil
	}
	out := make([]string, 0, len(snap))
	for _, o := range snap {
		out = append(out, fmt.Sprintf("%-44s %6.1f tok/s  (n=%d)", o.Model, o.TokensPerSec, o.Samples))
	}
	return out
}

// doctorHTTPStatusRe pulls the HTTP status out of a provider probe message.
var doctorHTTPStatusRe = regexp.MustCompile(`HTTP (\d{3})`)

// doctorRemedy classifies a failed provider check through cli.Remediation,
// which knows what each transport error and HTTP status actually means.
func doctorRemedy(check readiness.Check) (cause, remedy string) {
	if check.OK {
		return "", ""
	}
	status := 0
	if m := doctorHTTPStatusRe.FindStringSubmatch(check.Message); m != nil {
		status, _ = strconv.Atoi(m[1])
	}
	provider, _ := check.Details["provider"].(string)
	// model is deliberately empty: this probe calls /v1/models, so a 404 means
	// the base URL is wrong, never that one model id is missing.
	const model = ""
	if strings.Contains(strings.ToLower(check.Message), "no models") {
		return "the endpoint answered but listed no models",
			"it may not be an OpenAI-compatible server, or no model is loaded — try `curl " +
				check.Endpoint + "/models` and load a model"
	}
	// Only override readiness's own hint when we can classify the failure.
	// cli.Remediation has a catch-all ("check the endpoint with slmcode
	// doctor") that is strictly worse than the check's own advice for the
	// cases readiness diagnoses itself, e.g. "model not listed".
	if status == 0 && !transportFailure(check.Message) {
		return "", ""
	}
	return cli.Remediation(provider, check.Endpoint, model, status, check.Message)
}

// transportFailure reports a dial/DNS/TLS/timeout failure — the errors
// cli.Remediation turns into a specific instruction.
func transportFailure(msg string) bool {
	l := strings.ToLower(msg)
	for _, s := range []string{
		"connection refused", "no such host", "dns", "timeout",
		"deadline exceeded", "certificate", "tls",
	} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// providerRejectedAuth reports a 401/403 from the provider probe.
func providerRejectedAuth(check readiness.Check) bool {
	if check.OK {
		return false
	}
	m := doctorHTTPStatusRe.FindStringSubmatch(check.Message)
	if m == nil {
		return false
	}
	code, _ := strconv.Atoi(m[1])
	return code == 401 || code == 403
}
