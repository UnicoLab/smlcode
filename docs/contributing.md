# 🤝 Contributing

Public baseline on purpose. The most valuable contributions are the ones that make **small
models** more reliable: tighter tool contracts, better prompts, evals, gates that fail closed.

The authoritative guide — build steps, the lint ratchet, the test layout, how to add a
block/agent/skill/pack, and the package ownership map — is
[**CONTRIBUTING.md**](https://github.com/UnicoLab/smlcode/blob/main/CONTRIBUTING.md) in the repo
root. Code conventions are in [Conventions](conventions.md).

---

## Dev setup

```bash
git clone https://github.com/UnicoLab/smlcode.git && cd smlcode
make bootstrap        # build the Studio UI (npm ci + vite build) — go build alone
                      # embeds a placeholder page, not Studio
make check            # the one gate — same as CI
make install-user     # → ~/.local/bin/slmcode
```

`make check` runs `tidy` → `lint` (gofmt, `go vet`, golangci-lint, embedded-UI smoke) →
`test` (`go test ./...`) → `race` (`go test -race ./pkg/...`) → `npm run lint && npm run build`
in `web/`. CI's lint-test job and `.pre-commit-config.yaml` run exactly this.

## Docs site

```bash
make docs-serve    # http://127.0.0.1:8000
make docs-build    # strict build → site/
```

MkDocs Material, published to GitHub Pages. `mkdocs.yml` nav entries must resolve to real files —
`make docs-build` runs strict and fails otherwise.

If you change behaviour, change the page that documents it **in the same PR**. A doc that
overstates what the code does is worse than a missing one.

## Before a PR

- [ ] `make check` green
- [ ] `make docs-build` if you touched `docs/` or `mkdocs.yml`
- [ ] Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`…)
- [ ] Human commit messages — no tool trailers, no ANSI art
- [ ] No secrets (keys go in `.slmcode/auth.json` or the environment, never committed YAML)

## The lint ratchet

`make lint` runs golangci-lint **non-blocking** against a captured baseline recorded at the top of
`.golangci.yml`. Fix findings and **lower the baseline numbers**; use `make lint-strict` while
working a category. Do not add exclusion presets to get a green run — `.golangci.yml` sets none
deliberately, because with them on errcheck alone drops from 29 findings to 5, which is a
pre-filtered view rather than progress. `gofmt` and `go vet` are always blocking.

## Good first contributions

- SLM eval fixtures and trajectory recordings (`pkg/eval/metrics` replays them offline)
- JSON repair-ladder edge cases (`pkg/repair`)
- Seed repair rules for failure modes your model actually hits (`pkg/evolve/seed.go`)
- Language scanners for the repo map (`pkg/repomap/extract.go`)
- Provider presets and capability priors (`pkg/backends/capabilities.go`)
- Studio and TUI polish — stay offline, no CDN
- Docs recipes that fail less often than reality

## Links

- [GitHub](https://github.com/UnicoLab/smlcode)
- [Docs](https://unicolab.github.io/smlcode/)
- [Releases](https://github.com/UnicoLab/smlcode/releases)
- [UnicoLab](https://unicolab.ai)

☀️ Made with ♥ by [UnicoLab](https://unicolab.ai)
