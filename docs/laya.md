# External decision models: Laya and OpenAI-compatible servers

SLMCode can ask a separately served model for lightweight predictions. The Go
harness supports native [Laya](https://github.com/NandhaKishorM/laya) and
OpenAI-compatible chat completions. The service can run on the same machine or
on a remote host. No model runtime, weights, training scripts or automatic model
replacement are bundled with SLMCode.

The integration is **disabled by default**. Its transport and fallback behavior
are tested; a speedup or improvement in generated code is not established. Enable
it on representative projects and compare completion time, retries and test
outcomes before adopting it broadly.

## Where predictions are used

| Area | Implemented behavior | What remains authoritative |
| --- | --- | --- |
| Prior knowledge | Rerank up to eight memory, summary and learned-skill chunks before selecting the top K | Existing similarity floor and context budget |
| Navigation | Optional hint to locate definitions, callers and neighboring tests before editing | Focus files, tool permissions and anti-wander rules |
| Planning | Optional hint to make dependencies and specialist ownership explicit | Planner/splitter contracts and plan approval |
| Review and testing | Optional hint to check disk evidence, failure paths and regressions | Reviewers, tests and deterministic acceptance gates |
| Repair | Optional hint to investigate the cause and previous attempts before another edit | Corrector scope, retry limits and verification |

Only prior-knowledge reranking is enabled by setting an endpoint. The other uses
require `laya_guidance: true`. Guidance applies to recognized roles dispatched
through the orchestrator's gated executor, including worker/review/corrector
calls and single-pass planning calls. Multipass planning uses a separate path
and does not receive these hints. This is guidance for agent decisions; it does
not itself select agents, rank repository files, grant permission, skip tests,
approve changes or learn new weights.

## Connect to native Laya

Deploy upstream `laya-serve` separately using the
[upstream server instructions](https://github.com/NandhaKishorM/laya#http-server).
Its runtime dependencies and checkpoint downloads belong to that deployment.
Bind it to loopback for local use; use authentication and HTTPS when exposing it
across a network. `LAYA_API_KEY` configures the upstream bearer token.

Once the server is running, configure SLMCode:

```bash
slmcode config set laya_provider laya
slmcode config set laya_model multilingual
slmcode config set laya_endpoint http://127.0.0.1:8091
slmcode config set laya_timeout 2s
# Optional hints in addition to retrieval reranking:
slmcode config set laya_guidance true
```

Native Laya uses the **server root** as its endpoint. The Go client appends
`/v1/systemone` and sends `state`, `model`, and a `questions.relevant` question of
type `noul`. It reads `answers.relevant.noul`, requiring a number from 0 to 1 and
`type: noul`. Supported checkpoint aliases are `english`, `multilingual` and
`typed-decisions`. The question key is also used for guidance requests; the
question instructions determine the prediction being requested.

Check the service independently before running a project:

```bash
curl --fail --max-time 10 http://127.0.0.1:8091/health
curl --fail --max-time 30 http://127.0.0.1:8091/v1/systemone \
  -H 'Content-Type: application/json' \
  -d '{"model":"multilingual","state":"Task: fix JSON parsing. Candidate: parser error handling.","questions":{"relevant":{"type":"noul","instructions":"Is the candidate useful for the task?"}}}'
```

For an authenticated server, add `-H "Authorization: Bearer $SLMCODE_LAYA_API_KEY"`
to the inference request. A successful health check alone does not establish that
a checkpoint can answer predictions; warm it with an inference request too.

## Connect to an OpenAI-compatible decision model

Use an already-served chat model that supports JSON mode:

```bash
slmcode config set laya_provider openai
slmcode config set laya_model YOUR_EXACT_SERVED_MODEL_ID
slmcode config set laya_endpoint http://127.0.0.1:11434/v1
slmcode config set laya_timeout 10s
slmcode config set laya_guidance true
```

The example endpoint is Ollama's OpenAI-compatible API. For another server, use
its API base URL, including `/v1` or its custom prefix. SLMCode appends
`/chat/completions`; it does not infer or insert `/v1`. Model IDs retain their
case. Always set the served model ID when switching away from native Laya.

Requests use system/user messages, `temperature: 0`, `max_tokens: 64`,
`stream: false`, and `response_format: {"type":"json_object"}`. Responses must
contain one choice with `finish_reason: stop` and message content such as
`{"probability":0.85}`. Refusals, truncated output, prose/code fences, missing or
null scores, non-numeric values and scores outside 0–1 cause fallback. Models
that spend the output budget on reasoning may need a different serving preset
or a different decision model. A chat model's numeric answer is a score, not
measured or calibrated confidence.

Native Laya is not an OpenAI chat model. Selecting `openai` does not make its
checkpoint loadable in Ollama or oMLX. The two protocols are alternatives.

## Settings and credentials

| Setting | Default | Meaning |
| --- | --- | --- |
| `laya_provider` | `laya` | `laya` or `openai` |
| `laya_endpoint` | empty | Empty disables all prediction requests |
| `laya_model` | `multilingual` | Native alias or exact served chat model ID |
| `laya_timeout` | `2s` | Shared deadline per rerank operation; deadline per guidance request |
| `laya_guidance` | `false` | Enable fixed prediction-based hints for recognized roles |
| `laya_api_key` | empty | Separate bearer credential; no inheritance from the chat key |

All settings use the normal [configuration layers](config.md). Studio exposes
them in advanced retrieval settings. Prefer the environment variable
`SLMCODE_LAYA_API_KEY` for a secret; the public config masks it, and persistence
omits it unless `SLMCODE_PERSIST_API_KEY=1` is explicitly set. Other environment
names are `SLMCODE_LAYA_PROVIDER`, `SLMCODE_LAYA_ENDPOINT`, `SLMCODE_LAYA_MODEL`,
`SLMCODE_LAYA_TIMEOUT`, and `SLMCODE_LAYA_GUIDANCE`.

Requests send selected project knowledge or bounded prompt excerpts to the
configured endpoint. Choose that endpoint with the same data-handling care as
the main coding provider. The client refuses redirects and URLs with embedded
credentials, queries or fragments. Diagnostics never include provider response
bodies or authorization headers.

## Bounds, fallbacks and operating behavior

- Retrieval preserves the original similarity floor, original similarity scores,
  stable order on ties, and candidates beyond the first eight. Partial reranking
  is discarded on any failure. All candidate calls share one deadline.
- Retrieval rejects states above 640 estimated tokens (320 for native English).
  These are conservative harness limits, not exact checkpoint tokenization.
  Guidance uses labeled prefix/suffix excerpts within the same token limits.
- Guidance makes at most one prediction per recognized gated agent request.
  A score of at least 0.8 adds a fixed, harness-authored hint; lower scores add
  nothing. This threshold is a policy choice, not a calibrated confidence level.
- HTTP responses are limited to 64 KiB. There are no automatic retries. An error
  keeps normal retrieval/agent input and emits a warning. The parent run's
  cancellation also cancels predictions.
- Guidance executes inside the agent concurrency gate. It adds latency and may
  contend with the coding model if both use the same device. Its auxiliary calls
  are not included in the coding model's token-usage accounting.

Disable guidance with `slmcode config set laya_guidance false`. Disable all
predictions with `slmcode config set laya_endpoint ""`.

## Troubleshooting and release verification

| Symptom | Check |
| --- | --- |
| HTTP 404 | Native endpoint must be the server root; OpenAI endpoint must include its API prefix |
| HTTP 401 | Set the separate `SLMCODE_LAYA_API_KEY`; check the server's bearer configuration |
| Timeout / baseline used | Warm the checkpoint, measure inference latency and adjust `laya_timeout` |
| No hints | Guidance defaults off; scores below 0.8 abstain; unsupported roles and multipass planning bypass hints |
| No reranking calls | At least two candidates must pass the similarity floor |
| Invalid chat decision | Confirm JSON mode, sufficient serving output budget and the exact model ID |
| Candidate exceeds token bound | Baseline retrieval is preserved; reduce oversized memory chunks if appropriate |

The automated suite covers both wire formats, invalid responses, cancellation,
timeouts, redirect refusal, credential masking, stable ranking, fallback and
advisory dispatch. Before enabling a deployment, run a real inference smoke test
and a representative harness task with that exact server/checkpoint. Synthetic
HTTP fixtures cannot establish model accuracy or a production speedup. Training,
calibration, checkpoint rollout and rollback remain external operations.

From a source checkout, test the actual Go client against a running deployment:

```bash
SLMCODE_TEST_DECISION_ENDPOINT=http://127.0.0.1:8091 \
SLMCODE_TEST_DECISION_PROVIDER=laya \
SLMCODE_TEST_DECISION_MODEL=multilingual \
go test ./pkg/laya -run TestLiveDecisionServer -count=1 -v
```

For chat servers use `SLMCODE_TEST_DECISION_PROVIDER=openai`, the API base URL,
and the exact model ID. If authentication is required, set
`SLMCODE_TEST_DECISION_API_KEY` in the environment. Without an endpoint this test
explicitly skips; normal `make check` never requires a model download or a live
service. The smoke validates a real response within 30 seconds, not predictive
accuracy or downstream code quality.
