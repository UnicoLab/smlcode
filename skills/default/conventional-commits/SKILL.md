---
name: conventional-commits
description: Commit and PR messages that survive a `git log` six months later — Conventional Commits, imperative subjects, and what belongs in the body.
triggers: commit, commit message, changelog, pull request, pr description, semantic release, git
agents: worker, docs, memory, coordinator, planner
user-invocable: true
---

# Commit messages

```
<type>(<optional scope>): <imperative subject, <=72 chars, no trailing period>

Why the change was needed, and what the reader cannot see in the diff.
Wrap at 72 columns.

Refs: #123
BREAKING CHANGE: <what broke and what callers must do>
```

## Types

`feat` (a new capability — minor version) · `fix` (a bug — patch) ·
`docs` · `refactor` (no behaviour change) · `perf` · `test` · `build` · `ci` ·
`chore` (nothing a user sees).

A `!` after the type/scope (`feat(api)!:`) or a `BREAKING CHANGE:` footer marks
a major version. Both are read by semantic-release and by humans.

## Subject line

- **Imperative mood**: "add retry to the uploader", not "added" / "adds". It
  completes the sentence *"applied, this commit will …"*.
- **Say what changed, not which file.** `fix(auth): reject expired refresh
  tokens` beats `fix: update auth.go`.
- Lowercase after the colon, no trailing period, under 72 characters.

## Body

The diff already says WHAT. The body says **why now**, what alternatives were
rejected, and anything a future reader would otherwise have to reconstruct — the
bug's symptom, the ticket's constraint, the benchmark that justified the perf
work. Skip it entirely for a one-line obvious change rather than restating the
subject.

## Scope of one commit

One logical change. A commit that both fixes a bug and reformats the file makes
the fix unreviewable and unrevertable. Format separately.

## PR descriptions

Same subject rules for the title. Body: the problem, the approach, how it was
verified (the exact commands and their results), and anything the reviewer
should look at hardest. "Please review" is not a description.
