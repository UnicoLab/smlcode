# 🔒 Constrained decoding & provider capabilities

A frontier model emits valid JSON because it wants to. A 7B emits valid JSON because the decoder
will not let it do anything else. That difference is why SLMCode negotiates the strongest
constraint mechanism your endpoint actually supports, rather than asking nicely and cleaning up
afterwards.

Three packages: `pkg/schema` (contracts), `pkg/backends` (negotiation and transport),
`pkg/repair` (the fallback ladder).

---

## 1. Contracts (`pkg/schema`)

Every structured output the harness parses has a hand-written JSON Schema (draft-07 subset) plus
a derived GBNF grammar. The registered contract roles:

`plan` · `tasks` · `review` · `tester` · `clarify` · `escalate` · `composition` · `scope_judge` ·
`worker` · `explore` · `docs` · `architect` · `coordinator` · `orchestrator` · `placeholder` ·
`lessons`

Contracts are *output* names, not agent ids — several agents can emit the same contract, and a
`RoleSpec` names its contract with `SchemaRole` when its id does not match one.

The schemas deliberately stay inside the intersection that GBNF conversion **and** vLLM guided
decoding both support:

```
type · properties · required · items · enum · additionalProperties
minItems · maxItems · minLength · maxLength · minimum · maximum (integers)
```

Explicitly avoided: `uniqueItems`, `contains`, `if`/`then`/`else`, `prefixItems`,
`patternProperties`, `oneOf`/`anyOf`/`allOf`, `$ref` cycles, non-integer bounds. A schema that
cannot be expressed as a grammar is a schema that silently degrades on a local server.

A contract marked `Strict` is simple enough for OpenAI's `strict: true` `json_schema` mode: every
property required, `additionalProperties: false`, no free-form nesting.

`pkg/agents` keeps prompts and contracts in sync — `TestPromptContractsMatchSchema` fails if a
prompt promises a field the schema does not have.

## 2. Capability negotiation (`pkg/backends`)

### The ladder

| Rank | Mechanism | Wire form | Typically |
|---|---|---|---|
| 1 | `json_schema` | `response_format: {"type":"json_schema","json_schema":{…,"strict":true}}` | OpenAI, Azure, vLLM, LM Studio, Ollama, oMLX |
| 2 | `guided_json` | `guided_json: <schema>` (extra body field) | vLLM |
| 3 | `gbnf_grammar` | `grammar: <GBNF>` | llama.cpp, LM Studio |
| 4 | `json_object` | `response_format: {"type":"json_object"}` | almost everything |
| 5 | `prompt_only` | nothing on the wire | the floor |

The zero-value `Capabilities` is the weakest possible backend — prompt-only JSON with post-hoc
repair — so an unreachable or hostile endpoint degrades instead of failing.

### The probe

`backends.Probe(ctx, provider, endpoint, model, apiKey)`:

- starts from a **prior** for the provider preset (`PresetCapabilities`), which decides *which*
  probes are worth issuing at all — there is no point sending a `guided_json` probe to OpenAI;
- issues cheap probe requests against a trivial one-boolean schema;
- is memoised per `(provider, endpoint, model)` key in memory and, when a cache directory is set,
  in `capabilities.json`. Concurrent callers collapse onto one probe;
- never returns an error and never blocks longer than `ProbeTimeout` (20s — a cold local model
  can take a while to load, but a probe must never become the slow path);
- only a **successful probe** sets `Probed` and is trusted for decoding. A prior is a hint.

### Live demotion

Because a probe can be right at startup and wrong ten minutes later, the structured call path
walks the ladder downwards at request time. When a request that differs from a plain one *only*
by its constrained-decoding field comes back with a **permanent** rejection (4xx), that means the
server does not support the field: the capability is demoted for that key permanently, and the
next rung is tried. Transient failures, rate limits, cancellations and context overflows are not
demotions — they are returned as-is, because replaying them would double the attempts against a
local server that serialises inference anyway.

**Constrained decoding is never the reason a run fails.** If the whole ladder is exhausted, the
call falls back to the ordinary provider path and `pkg/repair` handles the output.

### Configuration

| Key | Values | Meaning |
|---|---|---|
| `structured_decoding` | `auto` (default), `off` | `auto` negotiates; `off` forces prompt-only JSON and relies on repair. Aliases for `off`: `none`, `false`, `0`, `prompt`, `prompt-only`. |

## 3. Decoding directives per role

`agents.NormalizeDecoding` fills in a role's decoding contract from its id, so a new role only
declares its tools:

- **Tool-using roles** get `SerialTools: true` and `JSONOnly: false`. Constrained decoding is not
  applied to a request that carries tools — the model needs room to emit a tool call.
- **Free-text roles** (`context`, `memory`, `describer`) get `JSONOnly: false` and no schema
  role: their output is markdown, and forcing JSON on them produces worse prose in a wrapper.
- **Everything else** with a schema role gets `JSONOnly: true` plus **stop sequences**
  (`"\n## "`, "```\n\n", `"\nNote:"`) that end the completion the moment the model starts writing
  a markdown section after its object. This is cheap and it works: the commonest small-model JSON
  failure is not malformed JSON, it is valid JSON followed by an essay.

**One tool call per turn** is enforced structurally: `SerialTools` truncates an assistant message
to its first tool call. The prompt asks for one; the transport guarantees it.

Language-specialised ids fold back to their generic role (`go-worker` → `worker`,
`python-tester` → `tester`), so YAML-defined agents inherit the right contract automatically.

## 4. The repair ladder (`pkg/repair`)

When output still arrives unconstrained, the ladder is tried in a fixed order, and the name of
the rung that fixed the document is returned — so the harness can learn which failure mode your
model actually has.

| Rung | Fixes |
|---|---|
| `none` | already valid |
| `fence` | ```` ```json … ``` ```` wrapper |
| `extract` | balanced object/array carved out of prose |
| `trailing_comma` | `,}` / `,]` |
| `quotes` | `'single'` → `"double"` |
| `python_bools` | `True`/`False`/`None` |
| `control_chars` | raw newline/tab inside a string |
| `close_braces` | missing `}` / `]` appended |
| `coerce` | schema-driven type coercion (`"true"`→`true`, `"3"`→`3`, scalar→one-element array) |

**Truncation is not malformation.** A document cut off mid-string returns `ErrTruncated`, not a
repair, because the correct response is to raise `max_tokens` or re-ask — appending closing
braces to a truncated string produces a document that parses and *lies*. `pkg/evolve` maps that
fingerprint to `action: raise_max_tokens` rather than a text fix.

`repair.Stats` counts rungs and outcomes, which is what feeds the "failures fixed from memory vs
from a fresh round-trip" metric.

## 5. Retry policy

`backends.Classify` buckets a failed call:

| Class | Trigger | Retried? |
|---|---|---|
| `transient` | connection-level failure, 5xx | ✅ — a local server that just finished loading a model refuses connections for a few seconds |
| `rate_limited` | 429, or an explicit `Retry-After` | ✅, honouring the hint |
| `permanent` | 400, 401, 403, 404, 413, 422 | ❌ — retrying burns a full prefill for nothing |
| `context_overflow` | `context_length_exceeded` 400 | ❌ — shrink the pack or raise the window instead |
| `canceled` | context cancellation/deadline, deliberate early stream exit | ❌ |
| `unknown` | unclassifiable | ❌ — treated as permanent so a broken request surfaces rather than being replayed three times |

`DefaultRetryPolicy` is 3 attempts, 500ms base, 20s ceiling, exponential with **full jitter**. A
server `Retry-After` hint always wins, clamped to the ceiling. Jitter matters here: with
`max_parallel: 4` against one local server, lockstep retries turn a recovery into a thundering
herd on a backend that serialises inference anyway.

The provider's own fixed-delay retry is registered with `RetryCount 0` so a request is never
retried twice over. `llm_retry_count` / `llm_retry_delay_ms` remain in config for the ordinary
provider path.

## 6. Debugging

```bash
slmcode doctor              # provider, model, endpoint, reachability
slmcode doctor --json
slmcode readiness           # scores SLM-safe settings; --fix applies them
slmcode status --json | jq .connection
```

If structured output is misbehaving, the first question is which mechanism was selected. Force
the floor with `slmcode config set structured_decoding off` and compare — if quality collapses,
constrained decoding was doing real work; if nothing changes, the endpoint was already at
`prompt_only` and the probe will say why.

Common causes of a silent demotion to `prompt_only`:

- an OpenAI-compatible proxy that returns 400 for unknown body fields;
- a model whose server advertises `json_schema` but rejects `strict: true`;
- an endpoint that is unreachable during the probe window (the zero value is the floor).

See also [Troubleshooting](troubleshooting.md).
