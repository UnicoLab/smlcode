---
name: honest-verification
description: Prove the objective was met, not merely that nothing is red — a suite that was already green is not evidence.
triggers: verify, done, complete, passed, finished, acceptance, impossible
agents: worker, tester, reviewer, corrector, deep, coordinator
user-invocable: true
---

# Honest verification

Measured: on a deliberately impossible task against a repository whose suite
**already passed**, 2 of 7 local-model runs reported success. Every one of the
false successes was a *short* run — the model edited a file, the green suite
stayed green, and nothing had actually been checked.

## The rule

**Green is only evidence if it was red before, or if the check is new.**

Before claiming success, answer these in order:

1. **Was the suite passing before I started?**
   If yes, it passing now proves nothing about my change.
2. **What did I run that specifically exercises the change?**
   Name the command and the test. "`go test ./...` passed" is not enough when it
   passed at baseline too.
3. **Would this command have failed if I had done nothing?**
   If no, it is not acceptance evidence. Find or write one that would.

## When you cannot verify

Say so explicitly. Report what you changed, what you ran, and that the result
does not distinguish success from no-op:

```
Changed: mathx/add.go
Ran:     go test ./mathx/... (passes — but also passed before the change)
Status:  UNVERIFIED — no check distinguishes this change from doing nothing
```

An honest "unverified" is a correct answer. A confident "success" that nothing
measured is the single most expensive failure mode: it ends the run, and the
work does not exist.

## When the task cannot be done

Some tasks are impossible as stated — mutually contradictory requirements, or a
constraint that forbids the only route. **Report that.** Do not:

- edit, delete or skip a test to make the assertion pass;
- add a build tag, environment switch or special case that hides the conflict;
- narrow the task to the part that is achievable and report success.

State plainly which two requirements cannot both hold, and stop. The harness
restores protected files that were modified anyway, so rewriting a test does not
even achieve the false result — it just costs the run.

## Do not keep working when it is done

The mirror-image failure. Before starting another edit pass, check that the
objective is not already met:

- the acceptance command passes **and** it failed at baseline → done, stop;
- every file the task named is changed and the checks pass → done, stop.

Continuing past that burns the budget that the *next* real problem needs.
