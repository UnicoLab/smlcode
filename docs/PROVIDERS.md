# 🔌 Providers — SLMCode ♥ any LLM

SLMCode is **SLM-first**, but it is a **generic coding harness**.
If a model speaks **OpenAI Chat Completions** (or Ollama’s native API), you can plug it in.

Defaults love **oMLX** on Apple Silicon. Everything else is one flag (or Studio setting) away.

Made with ♥ by [UnicoLab](https://unicolab.ai)

---

## 🎯 Mental model

```text
  your model of choice
         │
         ▼
  provider + endpoint + model (+ api key)
         │
         ▼
  SLMCode harness  →  plan / specialists / critic / memory
```

The harness does the hard parts for small models (atomic tasks, evidence gates,
repair, multipass). Bigger models still benefit — they just need fewer retries.

---

## ⚡ Quick switches

```bash
# Local oMLX (default)
slmcode config set provider omlx
slmcode config set model Qwen3-Coder-30B-A3B-Instruct-MLX-4bit

# Ollama
slmcode run --provider ollama --model qwen2.5-coder:14b \
  --endpoint http://127.0.0.1:11434 "fix the flaky test"

# LM Studio / vLLM / any OpenAI-compat gateway
slmcode run --provider lmstudio --model local-coder \
  --endpoint http://127.0.0.1:1234/v1 "…"

# Cloud OpenAI
slmcode run --provider openai --model gpt-4o-mini \
  --endpoint https://api.openai.com/v1 --api-key "$OPENAI_API_KEY" "…"

# OpenRouter (many frontier + open models)
export SLMCODE_PROVIDER=openrouter
export SLMCODE_MODEL=anthropic/claude-3.5-sonnet
export SLMCODE_ENDPOINT=https://openrouter.ai/api/v1
export SLMCODE_API_KEY=…   # or OPENROUTER_API_KEY
slmcode run -v "…"
```

Persist in the project:

```bash
slmcode config set provider ollama
slmcode config set model qwen2.5-coder:14b
slmcode config set endpoint http://127.0.0.1:11434
slmcode doctor
```

Or use **Studio → Settings** (provider / model / endpoint) — same config file.

---

## 🧩 Built-in presets

| Provider | Default endpoint | Notes |
|----------|------------------|-------|
| `omlx` | `http://127.0.0.1:8000/v1` | Apple Silicon local (default) |
| `ollama` | `http://127.0.0.1:11434` | Native Ollama API |
| `lmstudio` | `http://127.0.0.1:1234/v1` | OpenAI-compat |
| `openai` | `https://api.openai.com/v1` | Needs API key |
| `openrouter` | `https://openrouter.ai/api/v1` | Many models behind one key |
| `groq` | `https://api.groq.com/openai/v1` | Fast inference |
| `together` | `https://api.together.xyz/v1` | Open models |
| `deepseek` | `https://api.deepseek.com/v1` | |
| `mistral` | `https://api.mistral.ai/v1` | |
| `fireworks` | `https://api.fireworks.ai/inference/v1` | |
| `gemini` / `google` | Google OpenAI-compat URL | |
| `vllm` / `litellm` / `custom` | `http://127.0.0.1:8000/v1` | Self-hosted gateways |
| **anything else** | you set `endpoint` | Treated as OpenAI-compatible |

Unknown provider names are **kept as-is** and treated as OpenAI-compatible.
That means your private gateway just works:

```bash
slmcode run --provider my-corp-gateway \
  --endpoint https://llm.internal.example/v1 \
  --model corp-coder-32b \
  --api-key "$CORP_LLM_KEY" "…"
```

---

## 🔑 API keys

Resolution order (simplified):

1. `--api-key` / `config.api_key`
2. `SLMCODE_API_KEY`
3. Provider-specific env (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `OMLX_API_KEY`, …)
4. oMLX: `~/.omlx/settings.json`

Never commit keys. Prefer env vars or your secret manager.

---

## 👥 Per-agent providers

Different specialists can hit different backends (cheap local explorer + stronger
cloud reviewer, etc.):

```bash
# TUI
/agent edit reviewer provider=openai model=gpt-4o-mini endpoint=https://api.openai.com/v1
/agent edit worker provider=ollama model=qwen2.5-coder:14b
```

Or Studio → Agents. YAML lives under `.slmcode/agents/<id>.yaml`.

When endpoints differ, the runtime registers unique backend keys so agents never
accidentally share the wrong gateway.

---

## 🧠 Tips by model size

| Model class | Knobs that help |
|-------------|-----------------|
| Small SLM (7–14B) | `think_passes 2`, lower `max_context_kb`, `retries 2+`, keep tasks atomic |
| Mid SLM (~30B) | Defaults are tuned here — enjoy |
| Frontier LLM | You can raise parallel / context; harness still keeps runs inspectable |

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

Shows active provider, model, endpoint reachability, and embedding mode.
Green lights → go ship. 🚀

Next: **[GUIDE.md](GUIDE.md)** · **[INSTALL.md](INSTALL.md)**
