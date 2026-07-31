# 🤝 Contributing

Public baseline on purpose. Bring prompts, gates, evals, and UX that make **small models** more reliable.
Puns welcome. Secrets are not. 🔐

<div class="slm-banner" markdown>
<span class="slm-banner__emoji">💚</span>
<p class="slm-banner__text" markdown>
<strong>House style:</strong> conventional commits, human messages, no tool trailers, no ANSI art in git history.
Your future bisect-self will buy you coffee.
</p>
</div>

---

## Dev setup 🛠️

```bash
git clone https://github.com/UnicoLab/smlcode.git
cd smlcode
make tidy && make lint && make test
make install
```

### Docs site 📚

```bash
make docs-serve    # http://127.0.0.1:8000
make docs-build    # strict → site/
```

Stack: **MkDocs Material**, custom CSS, GitHub Pages via Actions.
If you change docs, make them fun *and* accurate. Both. We’re greedy like that.

---

## Before a PR ✅

- [ ] `make lint && make test`
- [ ] `make docs-build`
- [ ] Conventional commits (`feat:`, `fix:`, `docs:`, …)
- [ ] No secrets
- [ ] Human commit messages (no tool trailers, no ANSI art)

---

## Good first contributions 🌱

- SLM eval fixtures
- JSON repair edge cases
- Studio/TUI polish (stay offline!)
- Provider presets
- Docs recipes that fail less often than reality 😅

---

## Links 🔗

- [GitHub](https://github.com/UnicoLab/smlcode)
- [Docs](https://unicolab.github.io/smlcode/)
- [UnicoLab](https://unicolab.ai)
- [Releases](https://github.com/UnicoLab/smlcode/releases)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
