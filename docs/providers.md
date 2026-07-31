# 🔌 Providers — bring your own brain

SLMCode is **SLM-first**, but it's a **generic coding harness**.
If a model speaks **OpenAI Chat Completions** (or Ollama's native API), you're invited to the party.

Defaults flirt with **oMLX** on Apple Silicon. Everything else is one flag, env var, or Studio toggle away.

---

## 🎯 Mental model

```mermaid
flowchart LR
  M[Your model] --> P[provider + endpoint + model + key]
  P --> H[SLMCode harness]
  H --> Out[Plan / specialists / critic / memory]
```

The harness owns structure. The model owns tokens.
Don't make the model also be the project manager — it already has a day job.

---

## ⚡ Quick switches

=== "oMLX"

    ```bash
    slmcode config set provider omlx
    slmcode config set model Qwen3-Coder-30B-A3B-Instruct-MLX-4bit
    slmcode doctor
    ```

=== "Ollama"

    ```bash
    slmcode run --provider ollama --model qwen2.5-coder:14b \
      --endpoint http://127.0.0.1:11434 "fix the flaky test"
    ```

=== "LM Studio / vLLM"

    ```bash
    slmcode run --provider lmstudio --model local-coder \
      --endpoint http://127.0.0.1:1234/v1 "…"
    ```

=== "OpenAI cloud"

    ```bash
    slmcode run --provider openai --model gpt-4o-mini \
      --endpoint https://api.openai.com/v1 \
      --api-key "$OPENAI_API_KEY" "…"
    ```

=== "OpenRouter"

    ```bash
    export SLMCODE_PROVIDER=openrouter
    export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
    export SLMCODE_ENDPOINT=https://openrouter.ai/api/v1
    export SLMCODE_API_KEY=…   # or OPENROUTER_API_KEY
    slmcode run -v "…"
    ```

Persist in the project (so you don't re-type your life story):

```bash
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set endpoint http://127.0.0.1:11434
```

Studio → **Settings** writes the same `.slmcode/config.yaml`.

---

## 🧩 Built-in presets

| Provider | Default endpoint | Notes |
|----------|------------------|-------|
| `omlx` | `http://127.0.0.1:8000/v1` | Apple Silicon local (default) |
| `ollama` | `http://127.0.0.1:11434` | Native Ollama API |
| `lmstudio` | `http://127.0.0.1:1234/v1` | OpenAI-compat |
| `openai` | `https://api.openai.com/v1` | Needs API key |
| `openrouter` | `https://openrouter.ai/api/v1` | Many models, one key |
| `groq` | `https://api.groq.com/openai/v1` | Speed-run mode |
| `together` | `https://api.together.xyz/v1` | Open models |
| `deepseek` | `https://api.deepseek.com/v1` | |
| `mistral` | `https://api.mistral.ai/v1` | |
| `fireworks` | `https://api.fireworks.ai/inference/v1` | |
| `gemini` / `google` | Google OpenAI-compat URL | |
| `vllm` / `litellm` / `custom` | `http://127.0.0.1:8000/v1` | Self-hosted |
| **anything else** | you set `endpoint` | Treated as OpenAI-compatible |

!!! tip "Unknown names are features"
    `provider: my-corp-gateway` is fine. Set `endpoint` + key. We won't lecture you about brand loyalty.

```bash
slmcode run --provider my-corp-gateway \
  --endpoint https://llm.internal.example/v1 \
  --model corp-coder-32b \
  --api-key "$CORP_LLM_KEY" "…"
```

---

## 🔑 API keys (without leaking them into git history)

Resolution order (simplified):

1. `--api-key` / `config.api_key`
2. `SLMCODE_API_KEY`
3. Provider-specific env (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `OMLX_API_KEY`, …)
4. oMLX: `~/.omlx/settings.json`

!!! danger "Please don't"
    Commit API keys. Your future incident review will not find it funny.

---

## 👥 Mix providers per agent

Cheap local explorer + sharper cloud reviewer? Yes.

```bash
# TUI
/agent edit reviewer provider=openai model=gpt-4o-mini endpoint=https://api.openai.com/v1
/agent edit worker provider=ollama model=qwen2.5-coder:14b
```

Or Studio → **Agents**. YAML lives in `.slmcode/agents/<id>.yaml`.

When endpoints differ, the runtime registers unique backend keys so agents don't accidentally phone the wrong gateway. (Awkward.)

---

## 🧠 Knobs by model size

| Model class | What helps |
|-------------|------------|
| Small SLM (7–14B) | `think_passes 2`, lower `max_context_kb`, `retries 2+`, keep tasks atomic |
| Mid SLM (~30B) | Defaults are tuned here — enjoy the ride |
| Frontier LLM | Raise parallel / context; harness still keeps runs inspectable |

```bash
slmcode config set think_passes 2
slmcode config set retries 2
slmcode config set parallel 2
slmcode config set max_context_kb 16
```

---

## 🩺 Doctor

```bash
slmcode doctor
```

Shows active provider, model, endpoint reachability, embedding mode.
Green lights → go ship. 🚀

Made with ♥ by [UnicoLab](https://unicolab.ai)
