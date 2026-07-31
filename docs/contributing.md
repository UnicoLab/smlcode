# 🤝 Contributing

SLMCode is intentionally a **public baseline**. Bring better prompts, tighter gates, smarter scheduling, new specialists, and evals — especially ones that make **small models** more reliable.

Made with ♥ by [UnicoLab](https://unicolab.ai)

---

## 🛠️ Dev setup

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make tidy
make lint
make test
make install   # or install-system
```

Docs site (this one!):

```bash
python3 -m venv .venv-docs
source .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve          # http://127.0.0.1:8000
mkdocs build --strict
```

---

## ✅ Before you PR

- [ ] `make lint && make test`
- [ ] Docs still build: `mkdocs build --strict`
- [ ] Conventional commits: `feat:`, `fix:`, `docs:`, `chore:`, …
- [ ] No secrets in the diff (yes, even “temporary” ones)

!!! warning "Commit messages"
    Keep them human. No tool trailers. No ANSI art from your pager. Future archaeologists will thank you.

---

## 💡 Good first contributions

- SLM-specific eval tasks / fixtures
- Better JSON repair edge cases
- Studio UX polish (still offline!)
- Provider presets for new gateways
- Docs examples that fail less often than reality

---

## 🔗 Links

- Repo: [github.com/UnicoLab/smlcode](https://github.com/UnicoLab/smlcode)
- UnicoLab: [unicolab.ai](https://unicolab.ai)
- Releases: [GitHub Releases](https://github.com/UnicoLab/smlcode/releases)
