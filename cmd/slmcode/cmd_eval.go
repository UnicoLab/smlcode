package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/eval"
)

func evalCmd() *cobra.Command {
	var outPath string
	var caseID string
	var realQueries bool
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run the live coding eval harness (requires a working LLM endpoint)",
		Long: `Runs canned coding cases against the configured provider/model and writes
a JSON report. Use for regression on 7–14B local models.

Examples:
  slmcode eval
  slmcode eval --real
  slmcode eval --case langgraph-class-template --real
  slmcode eval --case py-hello --out .slmcode/eval-report.json
  RUN_E2E=1 go test ./test/e2e -run TestLiveRealQueryLangGraph`,
		RunE: func(cmd *cobra.Command, args []string) error {
			h, err := openHarness()
			if err != nil {
				return err
			}
			cases := eval.DefaultCases()
			if realQueries {
				cases = eval.RealQueryCases()
			}
			if caseID != "" {
				pool := cases
				if !realQueries {
					// Allow selecting a real-query id even without --real.
					pool = append(append([]eval.Case{}, eval.DefaultCases()...), eval.RealQueryCases()...)
				}
				var filtered []eval.Case
				for _, c := range pool {
					if c.ID == caseID {
						filtered = append(filtered, c)
					}
				}
				if len(filtered) == 0 {
					return fmt.Errorf("unknown case %q", caseID)
				}
				cases = filtered
			}
			fmt.Println(cli.Accent("eval") + " " + cli.Dim(fmt.Sprintf("%d case(s) · %s/%s",
				len(cases), h.Config.Provider, h.Config.Model)))
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
			defer cancel()
			rep := eval.RunAll(ctx, cases, h.Config)
			if outPath == "" {
				outPath = filepath.Join(h.Config.SlmDir(), "eval-report.json")
			}
			if err := eval.WriteReport(outPath, rep); err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(rep)
			cli.KeyVal("report", outPath)
			cli.KeyVal("passed", fmt.Sprintf("%d", rep.Passed))
			cli.KeyVal("failed", fmt.Sprintf("%d", rep.Failed))
			if rep.Failed > 0 {
				return fmt.Errorf("%d eval case(s) failed", rep.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "report JSON path (default .slmcode/eval-report.json)")
	cmd.Flags().StringVar(&caseID, "case", "", "run a single case id")
	cmd.Flags().BoolVar(&realQueries, "real", false, "run real-user query suite (LangGraph template, FastAPI, CLI)")
	return cmd
}
