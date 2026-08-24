// Package autoresearch is a ratchet loop that optimizes slmcode's OWN prompts
// and knobs.
//
// The shape is deliberately the one from the autoresearch literature:
//
//	snapshot → propose ONE change → apply → evaluate → keep if better,
//	else restore → record → repeat
//
// and every part of that sentence is load-bearing.
//
// # One change per iteration
//
// A Proposer returns exactly one Change: one knob, one new value. Two
// simultaneous edits produce a measurement nobody can attribute, so the
// deterministic proposer walks the surface coordinate-descent style and never
// bundles.
//
// # Bounded, and honest about why it stopped
//
// A run is capped on three axes at once — experiments, wall clock and tokens
// (Budget). When a cap is hit the run returns the best artifact so far AND a
// StopReason naming which cap ended it. Partial failure is reported, never
// smoothed over: "surface exhausted after 4 experiments" and "wall-clock budget
// spent after 4 experiments" are different facts and the caller gets to know
// which one happened.
//
// # Guarded against metric gaming
//
// A ratchet improves the metric it can see. Left alone it will happily find the
// knob that raises the pass rate by burning three times the tokens, or by
// turning off the checks that produce tool errors. So a change is retained only
// when the primary metric improves AND no guarded metric regresses beyond its
// tolerance — checked twice, against the current champion and against the run's
// own baseline, so a sequence of individually-tolerable regressions cannot
// accumulate into a large one. The default guard set (DefaultGuards) covers
// tokens per task, wall seconds per task, tool-error rate and edit-format apply
// rate. It is configurable and on by default.
//
// # Reversible without git
//
// Reversal is by file snapshot, never by version control: the harness has no
// git integration and must not start committing to somebody's repository as a
// side effect of an experiment. Every file a trial touches is copied first and
// copied back if the trial is not retained — with the restore in a defer, so an
// evaluator that panics, errors or gets its context canceled still leaves
// .slmcode/agents/*.yaml byte-for-byte as it was. A durable pre-run snapshot
// under .slmcode/autoresearch/snapshot/ covers the case the process cannot:
// `slmcode autoresearch --restore` undoes a finished run, including one that
// was killed outright.
//
// # Deterministic core, optional LLM
//
// The deterministic proposer is the product. A seed fixes the entire experiment
// sequence, so a run replays exactly. The LLM proposer (which can rewrite a
// system_prompt) is strictly additive: with no model configured the engine runs
// on the deterministic proposer alone, and a model that errors, stalls or
// answers with nonsense falls back to it rather than failing the run.
//
// # Opt-in
//
// Nothing here runs unless asked. The `autoresearch` config knob defaults to
// false and `slmcode autoresearch` defaults to a dry run.
//
// On-disk state lives entirely under .slmcode/autoresearch/ and
// `rm -rf .slmcode/autoresearch` is a supported operation.
package autoresearch
