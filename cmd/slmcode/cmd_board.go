package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/UnicoLab/slmcode/pkg/cli"
	"github.com/UnicoLab/slmcode/pkg/plan"
)

func boardCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "board",
		Aliases: []string{"b", "kanban"},
		Short:   "Show live kanban board (to_scope → … → done)",
		Example: "  slmcode board\n  slmcode board --json | jq '.tasks[] | .id'",
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonMode(asJSON)
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			_ = ws.Board.Load()
			b := ws.Board.Snapshot()
			if asJSON {
				by := b.ByColumn()
				counts := map[string]int{}
				for _, col := range plan.Columns() {
					counts[col] = len(by[col])
				}
				return emitJSON(map[string]any{
					"plan":    b.Plan,
					"columns": counts,
					"tasks":   b.Tasks,
				})
			}
			cli.Header("Kanban board")
			if b.Plan.Summary != "" {
				fmt.Println(cli.Dim("Plan: ") + b.Plan.Summary)
				fmt.Println()
			}
			by := b.ByColumn()
			for _, col := range plan.Columns() {
				tasks := by[col]
				fmt.Printf("%s %s\n", cli.ColumnColor("●"), cli.Bold(plan.ColumnLabel(col))+cli.Dim(fmt.Sprintf(" (%d)", len(tasks))))
				if len(tasks) == 0 {
					fmt.Println(cli.Dim("    —"))
					continue
				}
				for _, t := range tasks {
					done, total := t.ChecklistProgress()
					check := ""
					if total > 0 {
						check = cli.Dim(fmt.Sprintf(" [%d/%d]", done, total))
					}
					fmt.Printf("    %s  %s  %s%s\n",
						cli.Accent(t.ID),
						t.Title,
						cli.Dim("@"+t.Role),
						check,
					)
				}
			}
			fmt.Println()
			fmt.Println(cli.Dim("Tip: slmcode task move T1 ready_to_dev · slmcode task add \"…\""))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "task",
		Aliases: []string{"t"},
		Short:   "Add / edit / move / delegate / checklist tasks (live while agents run)",
	}

	var (
		column     string
		role       string
		desc       string
		acceptance string
		files      string
		notes      string
		priority   int
	)

	add := &cobra.Command{
		Use:   "add [title]",
		Short: "Create an atomic task on the board",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			bs := ws.Board
			_ = bs.Load()
			title := strings.Join(args, " ")
			return bs.Update(func(b *plan.Board) error {
				t := plan.Task{
					ID:          b.NextID(),
					Title:       title,
					Description: desc,
					Role:        role,
					Acceptance:  acceptance,
					Notes:       notes,
					Priority:    priority,
				}
				if column == "" {
					column = plan.ColToScope
				}
				t.MoveTo(column)
				if role != "" {
					t.Delegate(role)
				}
				if files != "" {
					t.Files = splitCSV(files)
				}
				t.Normalize()
				b.Tasks = append(b.Tasks, t)
				fmt.Println(cli.Success(fmt.Sprintf("added %s → %s", t.ID, t.Column)))
				return nil
			})
		},
	}
	add.Flags().StringVar(&column, "column", plan.ColToScope, "kanban column")
	add.Flags().StringVar(&role, "role", plan.RoleWorker, "specialist role to delegate")
	add.Flags().StringVar(&desc, "desc", "", "description / instructions")
	add.Flags().StringVar(&acceptance, "acceptance", "", "acceptance criteria")
	add.Flags().StringVar(&files, "files", "", "comma-separated focus files")
	add.Flags().StringVar(&notes, "notes", "", "human notes for the agent")
	add.Flags().IntVar(&priority, "priority", 3, "1=high … 5=low")

	show := &cobra.Command{
		Use:   "show [id]",
		Short: "Show one task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			_ = ws.Board.Load()
			t, ok := ws.Board.GetTask(args[0])
			if !ok {
				return fmt.Errorf("task %s not found", args[0])
			}
			cli.Header(t.ID + " — " + t.Title)
			cli.KeyVal("column", t.Column)
			cli.KeyVal("role", t.Role)
			cli.KeyVal("status", t.Status)
			cli.KeyVal("priority", fmt.Sprintf("%d", t.Priority))
			if len(t.Files) > 0 {
				cli.KeyVal("files", strings.Join(t.Files, ", "))
			}
			if t.Acceptance != "" {
				cli.KeyVal("acceptance", t.Acceptance)
			}
			fmt.Println()
			fmt.Println(t.Description)
			if len(t.Checklist) > 0 {
				fmt.Println()
				fmt.Println(cli.Bold("Checklist"))
				for _, c := range t.Checklist {
					mark := cli.Dim("[ ]")
					if c.Done {
						mark = cli.Green("[x]")
					}
					fmt.Printf("  %s %s %s\n", mark, c.Text, cli.Dim("("+c.ID+")"))
				}
			}
			if t.Notes != "" {
				fmt.Println()
				fmt.Println(cli.Bold("Notes"))
				fmt.Println(t.Notes)
			}
			return nil
		},
	}

	move := &cobra.Command{
		Use:   "move [id] [column]",
		Short: "Move task across kanban (works mid-run)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			return ws.Board.Update(func(b *plan.Board) error {
				t, ok := b.Get(args[0])
				if !ok {
					return fmt.Errorf("task %s not found", args[0])
				}
				t.MoveTo(args[1])
				b.UpdateTask(t)
				fmt.Println(cli.Success(fmt.Sprintf("%s → %s", t.ID, t.Column)))
				return nil
			})
		},
	}

	delegate := &cobra.Command{
		Use:   "delegate [id] [role]",
		Short: "Assign specialist: worker|reviewer|explorer|tester|corrector",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			return ws.Board.Update(func(b *plan.Board) error {
				t, ok := b.Get(args[0])
				if !ok {
					return fmt.Errorf("task %s not found", args[0])
				}
				t.Delegate(args[1])
				b.UpdateTask(t)
				fmt.Println(cli.Success(fmt.Sprintf("%s delegated to @%s", t.ID, t.Role)))
				return nil
			})
		},
	}

	edit := &cobra.Command{
		Use:   "edit [id]",
		Short: "Edit title/description/notes/acceptance flags",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			return ws.Board.Update(func(b *plan.Board) error {
				t, ok := b.Get(args[0])
				if !ok {
					return fmt.Errorf("task %s not found", args[0])
				}
				if desc != "" {
					t.Description = desc
				}
				if acceptance != "" {
					t.Acceptance = acceptance
				}
				if notes != "" {
					t.Notes = notes
				}
				if files != "" {
					t.Files = splitCSV(files)
				}
				if role != "" {
					t.Delegate(role)
				}
				if column != "" {
					t.MoveTo(column)
				}
				b.UpdateTask(t)
				fmt.Println(cli.Success("updated " + t.ID))
				return nil
			})
		},
	}
	edit.Flags().StringVar(&column, "column", "", "move to column")
	edit.Flags().StringVar(&role, "role", "", "delegate role")
	edit.Flags().StringVar(&desc, "desc", "", "replace description")
	edit.Flags().StringVar(&acceptance, "acceptance", "", "acceptance")
	edit.Flags().StringVar(&files, "files", "", "focus files")
	edit.Flags().StringVar(&notes, "notes", "", "human notes")

	check := &cobra.Command{
		Use:   "check [id] [item text or cN]",
		Short: "Tick a checklist item (create if text is new)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return checklistToggle(args[0], strings.Join(args[1:], " "), true)
		},
	}
	uncheck := &cobra.Command{
		Use:   "uncheck [id] [item id or text]",
		Short: "Untick a checklist item",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return checklistToggle(args[0], strings.Join(args[1:], " "), false)
		},
	}

	rm := &cobra.Command{
		Use:   "rm [id]",
		Short: "Remove a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			return ws.Board.Update(func(b *plan.Board) error {
				if !b.RemoveTask(args[0]) {
					return fmt.Errorf("task %s not found", args[0])
				}
				fmt.Println(cli.Success("removed " + args[0]))
				return nil
			})
		},
	}

	promote := &cobra.Command{
		Use:   "promote [id]",
		Short: "Shortcut: move to ready_to_dev (agents will pick it up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			return ws.Board.Update(func(b *plan.Board) error {
				t, ok := b.Get(args[0])
				if !ok {
					return fmt.Errorf("task %s not found", args[0])
				}
				t.MoveTo(plan.ColReadyToDev)
				b.UpdateTask(t)
				fmt.Println(cli.Success(t.ID + " promoted → ready_to_dev"))
				return nil
			})
		},
	}

	cmd.AddCommand(add, show, move, delegate, edit, check, uncheck, rm, promote)
	return cmd
}

func checklistToggle(taskID, item string, done bool) error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	return ws.Board.Update(func(b *plan.Board) error {
		t, ok := b.Get(taskID)
		if !ok {
			return fmt.Errorf("task %s not found", taskID)
		}
		if !t.SetChecklistItem(item, done, "") {
			// try as new text
			if strings.HasPrefix(item, "c") && t.SetChecklistItem(item, done, "") {
				// already handled
			} else {
				c := t.AddChecklist(item)
				c.Done = done
				t.SetChecklistItem(c.ID, done, item)
			}
		}
		b.UpdateTask(t)
		state := "unchecked"
		if done {
			state = "checked"
		}
		fmt.Println(cli.Success(fmt.Sprintf("%s checklist %s", t.ID, state)))
		return nil
	})
}

func contextCmd() *cobra.Command {
	showCtx := func(cmd *cobra.Command, args []string) error {
		ws, err := openWorkspace()
		if err != nil {
			return err
		}
		body, _ := ws.Store.Read("CONTEXT.md")
		cli.Header("CONTEXT.md")
		fmt.Println(body)
		return nil
	}
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"ctx"},
		Short:   "Show / edit working CONTEXT.md (safe while agents run)",
		RunE:    showCtx,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print CONTEXT.md",
		RunE:  showCtx,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "edit",
		Short: "Open CONTEXT.md in $EDITOR",
		RunE: func(cmd *cobra.Command, args []string) error {
			return editDoc("CONTEXT.md")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "append [text...]",
		Short: "Append a dated note to CONTEXT.md",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			if err := ws.Store.Append("CONTEXT.md", "Human note", strings.Join(args, " ")); err != nil {
				return err
			}
			fmt.Println(cli.Success("appended to CONTEXT.md"))
			return nil
		},
	})
	return cmd
}

func docsCmd() *cobra.Command {
	listDocs := func(cmd *cobra.Command, args []string) error {
		cli.Header("Markdown memory")
		for _, n := range []string{"PROJECT.md", "CONTEXT.md", "QUERY.md", "PLAN.md", "TASKS.md", "MEMORY.md", "SKILLS.md", "SCRATCH.md", "board.json"} {
			fmt.Println("  " + cli.Accent(n))
		}
		return nil
	}
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "List / show / edit .slmcode markdown memory",
		RunE:  listDocs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List memory documents",
		RunE:  listDocs,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show [name]",
		Short: "Print a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			body, err := ws.Store.Read(args[0])
			if err != nil {
				return err
			}
			cli.Header(args[0])
			fmt.Println(body)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "edit [name]",
		Short: "Edit a document in $EDITOR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return editDoc(args[0])
		},
	})
	return cmd
}

func planCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Show current PLAN.md",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace()
			if err != nil {
				return err
			}
			body, _ := ws.Store.Read("PLAN.md")
			cli.Header("PLAN.md")
			fmt.Println(body)
			return nil
		},
	}
}

func editDoc(name string) error {
	ws, err := openWorkspace()
	if err != nil {
		return err
	}
	_ = ws.EnsureInitialized()
	path := ws.Store.Path(name)
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, path)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
