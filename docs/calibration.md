# Calibration

slmcode measures your endpoint instead of guessing from its name.

!!! tip "What the measurements showed"
    [SLM learnings](slm-learnings.md) collects every figure these probes have
    produced across 59 runs and 4 models — throughput vs architecture, the
    concurrency knee, run-to-run dispersion — and names the mechanism each one
    changed.

A provider string like `omlx` or `openai` tells you nothing about how many
requests the server runs at once, how fast it decodes, or how big its context
window is. Those are properties of the machine in front of you, they change
when you swap models or hardware, and all three are observable in seconds. So
slmcode observes them.

```
slmcode calibrate
```

```
▸ Calibrate
─────────────
  provider        omlx
  model           Qwen3.8-27B-4bit
  endpoint        http://127.0.0.1:8000
  probing concurrency 1, 2, 4 (8 only if 4 still scales) — bounded at 1m0s

  max_parallel    2  (efficiency floor 60%)
  latency         p50 3.089s  p95 3.296s  (3 solo sample(s), 16-token completion)
  throughput      5.1 tokens/sec
  queueing        1.55x per-request latency at max_parallel=2
  context         262144 tokens (GET /v1/models max_model_len)

  concurrency   wall     per-request   throughput   efficiency
    1           3.119s   3.119s        1.00x        100%
  ← 2           5.071s   4.847s        1.23x        62%
    4           7.686s   7.431s        1.62x        41%

✔ measured in 25.539s
```

The table is printed on purpose. "`max_parallel` 2" on its own is
indistinguishable from a hardcoded guess — which is exactly what this replaces.
With the levels shown you can see that four-way ran at 41% of ideal and judge
the choice yourself.

## What it measures

| Measurement | How | Used for |
|---|---|---|
| **Concurrency knee** | 1, 2, 4 (8 only if 4 still scales) identical 16-token completions; efficiency = `wall(1) / wall(n)` | `max_parallel` |
| **Latency baseline** | p50/p95 of the solo calls | seeds role timeouts, `task_timeout` guidance |
| **Throughput** | server-reported `usage.completion_tokens` ÷ measured time | per-call deadlines (`backends.EstimateTimeout`) |
| **Context window** | `GET /v1/models` (`max_model_len`, `max_context_length`, `context_length`, `context_window`) | the context pack budget |

A throwaway warm-up call runs first, and its time is discarded — otherwise a
cold model's weight-load would be recorded as latency.

The context window is **read**, not probed. Nearly every OpenAI-compatible
server reports it, and binary-searching a 30B with real generations would cost
minutes of GPU time for a number the server will simply hand over. If the
server reports nothing, the shipped `model_profiles` heuristic stands.

## How the knee is chosen

Efficiency at concurrency *n* is `wall(1) / wall(n)`: the fraction of ideal
linear scaling actually delivered. The chosen knee is the highest level still
at or above **60%**, with every lower level also clearing it.

60% is a threshold, not a fitted constant. It sits in the empty band between
what a partially-parallel local server delivers at its knee (~62-76% measured)
and what it delivers past it (~41-53%). Nothing hardcodes a concurrency value:

| Measured curve | Chosen |
|---|---|
| 100%, 68%, 39% (single local endpoint) | 2 |
| 100%, 95%, 92%, 85% (a server that really scales) | 8 |
| 100%, 50%, 25% (strictly serial) | 1 |

Escalation stops at the first level below the floor — there is no reason to
spend four more seconds proving that 8 is worse than 4 on a server that already
fell off at 4.

## When it runs

- **Automatically**, once, on first sight of an unseen `(model, endpoint)` pair,
  after the endpoint reachability probe and before the run starts.
- **Never twice** for the same pair in one process, and never per wave.
- **Again** when the stored profile ages out (30 days), when it was produced by
  an older calibrator, when the server now reports a different context window,
  or on `slmcode calibrate --force`.

It cannot hang a run. The whole probe is capped at 60 seconds, each call at
half that, and any failure falls through to the static defaults with one
warning line:

```
⚠ calibration skipped for Qwen3.8-27B-4bit @ http://127.0.0.1:8000
  (calibrate: endpoint did not answer) — using defaults: max_parallel=2
```

Turn it off with `calibrate: off` in config, or `SLMCODE_NO_CALIBRATE=1` as a
hard kill switch for CI and sandboxes.

## Your settings always win

A measurement improves on a **default**, never on a **choice**. Every consumer
checks first:

| Value | Applied when |
|---|---|
| `max_parallel` | you have not set it in a config file, env var, flag, patch or stack |
| `task_timeout` | you have not set it — and only ever *raised*, never lowered |
| context window | you have not written `model_profiles` yourself |

`slmcode calibrate` says so explicitly when it is standing down:

```
  max_parallel=4 is set explicitly and is left alone
```

## Where profiles live

`~/.slmcode/memory/calibration.json`, beside the other cross-project stores,
with a human-readable `CALIBRATION.md` mirror. What a server can do is a
property of your machine, not of one repository.

Profiles are keyed on the **exact model id** plus the endpoint's scheme, host
and port. Not the model *family*: `Qwen3.8-27B-4bit` and `Qwen3.8-9B-4bit` have
completely different knees and latencies, and merging them would produce
confidently wrong numbers. Two servers on the same box are distinguished by
port. No API key, path or query string is ever stored.

The store is bounded (50 profiles, newest kept), prunable, and corruption-safe:
an unreadable file is quarantined as `calibration.json.corrupt` and the store
starts empty. `rm -rf ~/.slmcode/memory` is fully supported — it costs one
re-measurement.

## What calibration does *not* do

slmcode has two learning layers, and calibration **seeds** them rather than
competing with them:

- **Calibration** handles what is measurable up front, cheaply, without doing
  real work: knee, latency, throughput, context window.
- **The bandit** ([self-improvement](self-improvement.md)) handles what is only
  learnable from *outcomes*: edit format, think passes, review strictness, role
  model. A 16-token synthetic completion is evidence about none of those, so
  calibration seeds **no** bandit posterior — `evolve.DefaultPriors` stays the
  authority there.
- **Role latency memory** keeps real per-role p95. Calibration seeds it only for
  roles with *no* observations yet, and real observations displace the seed
  within a run or two.

That last one is the delicate one, so it has three guardrails:

* the estimate is **proportional** — measured decode rate × the role's own
  token budget × measured queueing inflation. Seeding from the capability probe
  would not be: that probe requests `max_tokens=1`, so it measures connect and
  prefill, two orders of magnitude off a real role call and not proportional
  to it;
* it is **biased high**. A role phase is many model calls, not one, so the
  per-call estimate is multiplied by a turn allowance. Over-estimating costs
  one slow role; under-estimating costs a failed run, which is the failure this
  whole change exists to remove;
* it is **capped at your `task_timeout`**. For a slow local model the seed
  therefore lands exactly on today's cold-start behavior — the whole budget —
  and the real fix there is the wider `task_timeout` calibration recommends.
  The seed can only ever tighten a budget for a model fast enough that a role
  provably does not need the whole ceiling.

## Commands

```
slmcode calibrate                          # measure (or reuse) the active pair
slmcode calibrate --force                  # re-measure regardless
slmcode calibrate --show                   # print stored profiles, probe nothing
slmcode calibrate --json                   # machine-readable
slmcode calibrate --model <id>             # a specific model on this endpoint
slmcode doctor                             # the active profile, alongside the rest
```
