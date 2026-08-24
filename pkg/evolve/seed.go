package evolve

// SeedRules returns the built-in repair rules.
//
// These encode the failure modes slmcode already knows small models hit, so
// the harness is useful on day one rather than after a hundred runs. Each one
// is a rule the codebase's own tool layer already tries to explain in prose;
// as a Rule the explanation becomes something the harness can *apply*.
//
// Seeded rules start at a Beta(4,1) prior — believed, but still subject to
// evidence: if a repair keeps failing on your stack it loses confidence and
// eventually retires itself.
func SeedRules() []Rule {
	seeds := []struct {
		trigger Trigger
		repair  Repair
		note    string
	}{
		// --- ws_edit: the small-model bottleneck -------------------------
		{
			trigger: Trigger{Class: ClassEditLineNumbers},
			repair: Repair{
				Kind:      RepairTransformArgs,
				Transform: TransformStripLineNumbers,
				Retry:     true,
				Guidance: "ws_read prefixes every line with `   42| `. That gutter is display only — it is NOT in the file. " +
					"Strip the `<number>|` prefix from old_str and new_str before editing.",
			},
			note: "the single most common reason an old_str can never match",
		},
		{
			trigger: Trigger{Class: ClassEditNotFound, Tool: "ws_edit"},
			repair: Repair{
				Kind: RepairAction, Action: ActionRereadFile, Retry: true,
				Guidance: "old_str did not match. Do NOT guess a new old_str. ws_read the file again, copy 2–3 " +
					"consecutive lines VERBATIM from the output (without the line-number gutter), and retry with " +
					"that smaller, uniquely-anchored span.",
			},
			note: "re-read then retry with a smaller, uniquely anchored span",
		},
		{
			trigger: Trigger{Class: ClassEditNotFound, Tool: "ws_edit", MessageContains: []string{"closest text"}},
			repair: Repair{
				Kind: RepairTransformArgs, Transform: TransformTrimTrailingWS, Retry: true,
				Guidance: "old_str missed only on whitespace. Trailing spaces and line endings were normalized; retry the edit. " +
					"If it still misses, ws_read and copy the block verbatim.",
			},
			note: "whitespace drift is repairable without a model call",
		},
		{
			trigger: Trigger{Class: ClassEditAmbiguous},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "old_str matched in more than one place. Add 2–3 lines of surrounding context (the enclosing " +
					"func signature or the line above and below) so exactly one location matches. Only pass " +
					"replace_all:true if you genuinely intend to change every occurrence.",
			},
			note: "prefer a unique anchor over replace_all",
		},
		{
			trigger: Trigger{Class: ClassEditEmptyOldStr},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "old_str may not be empty. To CREATE a file use ws_write. To APPEND, set old_str to the last " +
					"2–3 lines currently in the file. To INSERT, set old_str to the 2–3 lines you want to insert before.",
			},
		},
		{
			trigger: Trigger{Class: ClassEditNoOp},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "old_str and new_str were identical, so the edit was a no-op. Either make a real change, or " +
					"stop editing and return your final status JSON — the file may already be correct.",
			},
		},
		{
			trigger: Trigger{Class: ClassFileNotRead},
			repair: Repair{
				Kind: RepairAction, Action: ActionRereadFile, Retry: true,
				Guidance: "The file must be ws_read in this session before it can be edited. Read it, then retry the " +
					"edit using text copied from that read.",
			},
		},
		{
			trigger: Trigger{Class: ClassPatchFailed},
			repair: Repair{
				Kind: RepairEditFormat, EditFormat: "search_replace", Retry: true,
				Guidance: "A diff hunk failed to apply. Multi-hunk diffs are brittle for small models: switch to " +
					"search/replace and make ONE edit at a time. If a single search/replace also fails, rewrite the " +
					"whole file with ws_write as a last resort.",
			},
			note: "diff → search/replace → whole file, in that order",
		},

		// --- output shape ------------------------------------------------
		{
			trigger: Trigger{Class: ClassMalformedJSON},
			repair: Repair{
				Kind: RepairTransformArgs, Transform: TransformRepairJSON, Retry: true,
				Guidance: "The JSON was malformed. It was repaired mechanically (fences, trailing commas, single " +
					"quotes, unbalanced braces). If it fails again, emit ONLY a JSON object — no prose, no ``` fence.",
			},
		},
		{
			trigger: Trigger{Class: ClassTruncatedOutput},
			repair: Repair{
				Kind: RepairAction, Action: ActionRaiseMaxTokens, Retry: true,
				Guidance: "The response was cut off by the token limit, so the tail is missing — do NOT try to guess " +
					"the missing part. Raise max_tokens for the retry, or shorten the response by editing one file " +
					"at a time.",
			},
			note: "never guess past a truncation",
		},
		{
			trigger: Trigger{Class: ClassContextOverflow},
			repair: Repair{
				Kind: RepairAction, Action: ActionCompactContext, Retry: true,
				Guidance: "The prompt exceeded the model's context window. Compact the conversation (preserving files " +
					"read, files edited, commands run and open failures) and retry. If it overflows again, split the " +
					"task so fewer files are in scope.",
			},
		},

		// --- loop / progress ---------------------------------------------
		{
			trigger: Trigger{Class: ClassNoProgress},
			repair: Repair{
				Kind: RepairAction, Action: ActionForceDifferent, Retry: false,
				Guidance: "That exact tool call was already made and returned the same thing. Repeating it cannot help. " +
					"Take a DIFFERENT action: edit a file, run the test command, or finish with your status JSON.",
			},
		},
		{
			trigger: Trigger{Class: ClassReviewRejected},
			repair: Repair{
				Kind: RepairAction, Action: ActionSplitTask, Retry: false,
				Guidance: "The reviewer has rejected this repeatedly, which usually means the task is too big or the " +
					"acceptance criterion is unreachable. Split it into smaller file-scoped tasks, or restate the " +
					"acceptance criterion in checkable terms.",
			},
		},

		// --- environment ---------------------------------------------------
		{
			trigger: Trigger{Class: ClassPermissionDenied},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "That command is not permitted in the current permission mode. Do not retry it. Propose the " +
					"allowed equivalent (a read-only inspection, or the project's own build/test command) or ask the " +
					"user to approve it.",
			},
		},
		{
			// force_different_action, not guidance. The two cost the same at
			// the tool seam, but this failure is definitionally "the call you
			// made cannot succeed, take a different one" — the same shape as
			// the repeated-tool-call repair above — and naming it as an action
			// keeps it in the family the harness can route without a prompt,
			// with Retry:false so nothing re-issues the refused write.
			//
			// One rule covers every role because ClassOutOfScopeWrite is
			// structural: all variants share a fingerprint, so a second,
			// role-specific rule could be bound to that same fingerprint and
			// hand a worker the explorer's advice. The role's own contract is
			// already stated verbatim by the tool refusal this text is appended
			// to; the repair's job is only to stop the retry loop.
			trigger: Trigger{Class: ClassOutOfScopeWrite},
			repair: Repair{
				Kind: RepairAction, Action: ActionForceDifferent, Retry: false,
				Guidance: "The write was refused for WHERE it points, not for how it was written. Read-only roles " +
					"(explorer, docs, context, planner, splitter, reviewer) do not edit files at all; editing roles " +
					"write only inside their task focus files. Rewording the call, adding surrounding context, or " +
					"exploring more files cannot change that. Do the allowed thing instead — edit a focus file, or " +
					"report the change that is needed in your answer — and finish.",
			},
			note: "an out-of-scope write is a scope decision, never an edit-syntax problem",
		},
		{
			trigger: Trigger{Class: ClassFileNotFound},
			repair: Repair{
				Kind: RepairAction, Action: ActionRereadFile, Retry: false,
				Guidance: "That path does not exist. List the directory before assuming a layout — a hallucinated path " +
					"is the usual cause. Use ws_glob or ws_list to find the real one.",
			},
		},
		{
			trigger: Trigger{Class: ClassDependency},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "A required tool or module is missing from the environment. Do not work around it by writing " +
					"your own implementation. Report it, or use the project's documented setup command.",
			},
		},
		{
			trigger: Trigger{Class: ClassRateLimit},
			repair: Repair{
				Kind: RepairAction, Action: ActionBackoffRetry, Retry: true,
				Guidance: "The provider rate-limited the request. Back off and retry; do not reduce the quality of the " +
					"work to fit fewer calls.",
			},
		},
		{
			trigger: Trigger{Class: ClassTimeout},
			repair: Repair{
				Kind: RepairAction, Action: ActionSplitTask, Retry: false,
				Guidance: "The operation timed out. Split the task smaller, raise task_timeout, or lower max_parallel " +
					"to reduce contention on a local model.",
			},
		},

		// --- language-specific compile hygiene -----------------------------
		{
			trigger: Trigger{Class: ClassCompileError, Language: "go", MessageContains: []string{"declared and not used"}},
			repair: Repair{
				Kind: RepairGuidance,
				Guidance: "Go rejects unused local variables. Either use the variable or remove its declaration; " +
					"assigning to `_` is acceptable only when the call's side effect is the point.",
			},
		},
		{
			trigger: Trigger{Class: ClassCompileError, Language: "go", MessageContains: []string{"undefined"}},
			repair: Repair{
				Kind: RepairAction, Action: ActionRereadFile, Retry: false,
				Guidance: "An identifier is undefined. Grep for its real name before inventing one — it is usually a " +
					"typo, a missing import, or a symbol that lives in another package.",
			},
		},
		{
			trigger: Trigger{Class: ClassCompileError, Language: "python", MessageContains: []string{"indentation"}},
			repair: Repair{
				Kind: RepairTransformArgs, Transform: TransformUnfence, Retry: true,
				Guidance: "Python indentation broke, usually because a markdown code fence or a re-indented block was " +
					"pasted into the file. The fence was stripped; verify the block's indentation matches its scope.",
			},
		},
	}

	out := make([]Rule, 0, len(seeds))
	for _, s := range seeds {
		if err := s.repair.Validate(); err != nil {
			// A malformed seed is a programming error; drop it rather than
			// shipping a rule that can never fire.
			continue
		}
		out = append(out, Rule{
			ID:      RuleID(s.trigger, s.repair),
			Trigger: s.trigger,
			Repair:  s.repair,
			Scope:   ScopeBuiltin,
			Seeded:  true,
			Note:    s.note,
		})
	}
	return out
}
