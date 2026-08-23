// Package memory implements slmcode's four-layer memory subsystem.
//
// The layers, from shortest-lived to longest-lived:
//
//	Working    (run-scoped, in-process)  the current task, focus files, the last
//	                                     N tool calls, open failures, decisions
//	                                     and a rolling summary. Rendered into a
//	                                     terse prompt block under a token budget.
//	Episodic   (project, on disk)        one append-only JSONL record per turn:
//	                                     query, plan, files, tools, failures,
//	                                     gates, cost, verdict. Recalled by a
//	                                     deterministic BM25F-style scorer.
//	Semantic   (project, on disk)        distilled, deduplicated, confidence
//	                                     scored facts about THIS project.
//	Procedural (user, on disk)           what works for a given model family and
//	                                     language, across all projects.
//
// Design rules that the whole package obeys:
//
//   - Deterministic core. Nothing here needs an LLM. Distill accepts an
//     optional summarizer and is merely *better* with one.
//   - Bounded. Every collection has a cap, every store has a Prune policy, and
//     every prompt rendering is budgeted in tokens.
//   - Safe to be wrong. Every read path tolerates truncated, corrupt or
//     hand-edited files and returns a usable zero value. A memory failure must
//     never wedge a run, so the mutating API is best-effort and errors are
//     surfaced through Warnings rather than aborting a caller.
//   - Inspectable and reversible. Everything is human-readable JSON/JSONL/
//     Markdown under .slmcode/memory and ~/.slmcode/memory. `rm -rf` on either
//     directory is a supported operation.
package memory
