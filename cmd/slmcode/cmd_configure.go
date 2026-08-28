package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/autoconfig"
	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/config"
)

// `slmcode configure` — find a model server and write a config that works.
//
// Every piece of this existed and none of it was joined up. The harness could
// list an endpoint's models, measure what a (model, endpoint) pair can do and
// check whether the configured endpoint was answering — but nothing could
// answer the question a new user actually has, which is "what do I put in the
// config". They got a default endpoint that may not be running, a default model
// that may not be served, and a refusal at their first run.
//
// So this looks at what is actually running, asks it what it serves, picks the
// model best suited to writing code, and writes it down. Every step is
// printed, because a tool that silently rewrites your configuration is one you
// have to check afterwards anyway.

func configureCmd() *cobra.Command {
	var (
		asJSON    bool
		yes       bool
		toUser    bool
		dryRun    bool
		endpoint  string
		model     string
		timeoutS  string
		skipCalib bool
	)
	cmd := &cobra.Command{
		Use:     "configure",
		Aliases: []string{"setup", "autoconfig"},
		Short:   "Find a model server and write a working config",
		Long: `Find a model server and write a config that works.

Probes the configured endpoint first, then the addresses local model servers
listen on (oMLX, Ollama, LM Studio, vLLM), then any hosted provider whose API
key is already in the environment. Every candidate is tried at once, so a
machine with nothing running answers in a couple of seconds rather than waiting
out each address in turn.

Whatever answers is asked what it serves, and the model best suited to writing
code is chosen from its list — coder-tuned and instruction-tuned models first,
with embedding, speech, vision and safety models ruled out. Picking one of
those by accident is the failure this prevents: the harness runs, the model
answers, and nothing it says is JSON.

A working configuration is confirmed, never replaced: the endpoint you set is
probed first and kept if it answers.

Credentials stay where they belong. Your API key is sent only to the endpoint
you configured, or to a hosted provider offered because that provider's own key
is present — never to a local port that merely might be a model server.`,
		Example: `  slmcode configure                  # look around, ask before writing
  slmcode configure --yes            # accept the best candidate
  slmcode configure --dry-run        # show what it would write
  slmcode configure --json           # machine-readable
  slmcode configure --endpoint http://127.0.0.1:1234/v1
  slmcode configure --model Qwen3-Coder-30B-A3B-Instruct
  slmcode configure --user           # write to the user config, not this project`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			timeout := autoconfig.DefaultProbeTimeout
			if strings.TrimSpace(timeoutS) != "" {
				d, perr := time.ParseDuration(timeoutS)
				if perr != nil {
					return failf(2, "invalid --timeout %q: %v", timeoutS, perr)
				}
				timeout = d
			}

			cfg := ws.Config
			// An explicit --endpoint is an instruction, not a hint: probe that
			// and nothing else, so `configure --endpoint X` cannot quietly
			// settle on Y.
			probeCfg := *cfg
			if strings.TrimSpace(endpoint) != "" {
				probeCfg.Endpoint = endpoint
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout+7*time.Second)
			defer cancel()

			if !asJSON {
				fmt.Println(cli.Dim("Looking for a model server…"))
			}
			res := autoconfig.Discover(ctx, &probeCfg, os.Getenv, autoconfig.HTTPProber(timeout))
			if strings.TrimSpace(endpoint) != "" {
				res = onlyEndpoint(res, endpoint)
			}
			if !asJSON {
				fmt.Print(res.Explain())
			}

			if !res.Found {
				if asJSON {
					return emitJSON(map[string]any{
						"ok": false, "reason": res.NothingFound(), "tried": findingsJSON(res),
					})
				}
				fmt.Println()
				return failf(1, "%s", res.NothingFound())
			}

			choice := res.Choice
			// An explicit --model overrides the ranking, but only to something
			// the server actually serves: pinning a model that is not there
			// writes a config whose first run fails.
			if strings.TrimSpace(model) != "" {
				pinned, perr := pinModel(res, choice, model)
				if perr != nil {
					return perr
				}
				choice = pinned
			}

			if asJSON {
				return emitJSON(map[string]any{
					"ok": true, "choice": choice, "tried": findingsJSON(res),
					"written": !dryRun, "scope": scopeName(toUser),
				})
			}

			fmt.Println()
			fmt.Println(cli.Bold("  provider  ") + choice.Provider)
			fmt.Println(cli.Bold("  endpoint  ") + choice.Endpoint)
			fmt.Println(cli.Bold("  model     ") + choice.Model)
			fmt.Println(cli.Dim("            " + choice.Why))
			if len(choice.Others) > 0 {
				fmt.Println(cli.Dim("  also available: " + strings.Join(choice.Others, ", ")))
			}
			fmt.Println()

			if dryRun {
				fmt.Println(cli.Dim("--dry-run: nothing was written."))
				return nil
			}
			if !yes && !confirm("Write this to "+scopeName(toUser)+" config?", true) {
				fmt.Println(cli.Dim("Nothing was written."))
				return nil
			}

			if err := writeChoice(cfg, choice, toUser); err != nil {
				return err
			}
			fmt.Println(cli.Success("configured " + choice.Provider + " · " + choice.Model))

			if !skipCalib {
				fmt.Println(cli.Dim("Measuring what this endpoint can do — `slmcode calibrate --show` for the table."))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "accept the best candidate without asking")
	cmd.Flags().BoolVar(&toUser, "user", false, "write to the user-level config instead of this project")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be written and stop")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "probe only this endpoint")
	cmd.Flags().StringVar(&model, "model", "", "pin this model instead of the best-ranked one")
	cmd.Flags().StringVar(&timeoutS, "timeout", "", "per-candidate probe timeout (default 3s)")
	cmd.Flags().BoolVar(&skipCalib, "no-calibrate", false, "skip the follow-up calibration hint")
	return cmd
}

// onlyEndpoint narrows a result to the endpoint the user named.
//
// --endpoint is an instruction rather than a hint: without this, a probe of an
// address that is down would fall through to whatever else is running and
// `configure --endpoint X` would quietly settle on Y.
func onlyEndpoint(res autoconfig.Result, endpoint string) autoconfig.Result {
	want := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	var kept []autoconfig.Finding
	for _, f := range res.Findings {
		if strings.TrimRight(f.Endpoint, "/") == want {
			kept = append(kept, f)
		}
	}
	out := autoconfig.Result{Findings: kept}
	out.Choice, out.Found = autoconfig.Choose(kept)
	return out
}

// pinModel honors --model, refusing one the server does not serve.
func pinModel(res autoconfig.Result, choice autoconfig.Choice, model string) (autoconfig.Choice, error) {
	model = strings.TrimSpace(model)
	for _, f := range res.Findings {
		if !f.Live() || f.Endpoint != choice.Endpoint {
			continue
		}
		for _, name := range f.Models {
			if strings.EqualFold(name, model) {
				choice.Model = name
				choice.Why = "pinned with --model"
				return choice, nil
			}
		}
		return choice, failf(2, "%s does not serve %q — it serves: %s",
			f.Endpoint, model, strings.Join(f.Models, ", "))
	}
	return choice, failf(2, "no live endpoint to pin %q against", model)
}

func scopeName(user bool) string {
	if user {
		return "user"
	}
	return "project"
}

// writeChoice persists the three keys, at the scope the user asked for.
func writeChoice(cfg *config.Config, c autoconfig.Choice, toUser bool) error {
	if toUser {
		for _, kv := range []struct{ key, value string }{
			{"provider", c.Provider}, {"endpoint", c.Endpoint}, {"model", c.Model},
		} {
			field, ok := config.PatchableField(kv.key)
			if !ok {
				return failf(1, "%s is not writable", kv.key)
			}
			if err := setUserConfigValue(field, kv.value); err != nil {
				return err
			}
		}
		return nil
	}
	cfg.Provider = c.Provider
	cfg.Endpoint = c.Endpoint
	cfg.Model = c.Model
	// A detected provider is not a stack choice; leaving a stale highlight
	// pinned would make the Studio show a stack the config no longer matches.
	cfg.ActiveStack = ""
	cfg.Normalize()
	return cfg.Save()
}

func findingsJSON(res autoconfig.Result) []map[string]any {
	out := make([]map[string]any, 0, len(res.Findings))
	for _, f := range res.Findings {
		out = append(out, map[string]any{
			"provider": f.Provider, "endpoint": f.Endpoint, "reason": f.Reason,
			"models": f.Models, "live": f.Live(), "error": f.Err,
			"latency_ms": f.Latency.Milliseconds(),
		})
	}
	return out
}
