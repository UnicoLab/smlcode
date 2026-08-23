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
make bootstrap        # install web/ npm deps + build the Studio UI into cmd/slmcode/ui/
make check            # the one gate — same as CI
make install-user     # → ~/.local/bin/slmcode
```

### The Studio UI build

`make bootstrap` is the only step that needs Node (18+). It installs `web/`'s npm dependencies
and runs the Vite build into `cmd/slmcode/ui/`, which is `go:embed all:ui`ed into the binary.
`go build` alone always works — it just produces a binary with no SPA, which serves a built-in
placeholder page telling you to run `make bootstrap`. The CLI, the TUI and the Studio API are
unaffected.

Two things worth knowing before your first build:

- **`web/package-lock.json` is currently out of date with `web/package.json`** (it predates
  `vitest`, `@testing-library/*` and `eslint`), so `npm ci` refuses to run. `make bootstrap`
  detects this, says why, and falls back to `npm install` — which **regenerates the lock**.
  Commit the regenerated `web/package-lock.json`; that is what puts everyone back on `npm ci`.
- **`cmd/slmcode/ui/` holds exactly one tracked file, `.gitkeep`.** `index.html`, `assets/` and
  `vendor/` there are gitignored build output, so building the UI never dirties a tracked file.
  `.gitkeep` is what keeps `//go:embed all:ui` compiling on a fresh clone.

Rebuild after editing `web/` with `make ui-react`; live dev server with `cd web && npm run dev`.
The full story is in [CONTRIBUTING.md](https://github.com/UnicoLab/smlcode/blob/main/CONTRIBUTING.md#build).

`make check` runs `tidy-check` (`go mod tidy -diff`) → `lint` (gofmt, `go vet`, golangci-lint,
embedded-UI smoke) → `cover` (`go test ./...` with coverage, against the floor) →
`race` (`go test -race ./pkg/...`) → `web-check` (`lint`, `typecheck:test`, `test` and `build`
in `web/`).
CI's lint-test job and `.pre-commit-config.yaml` run exactly this.

The two steps that need the outside world — the Go module proxy and the npm registry — **skip
with a named reason** rather than failing, so `make check` is genuinely runnable everywhere. CI
has both and runs them for real.

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

## The lint ratchet — finished

The ratchet reached zero. `make lint` now runs golangci-lint **blocking**: any finding fails the
build. `make lint-strict` is an alias for `make lint`, kept because CI and muscle memory both
still say it.

Fix the finding — that is almost always right. If it is genuinely a false positive, add
`//nolint:<linter> // <why this site is a false positive>`; a bare `//nolint` is not accepted.
Do not add exclusion presets to get a green run: `.golangci.yml` sets none deliberately, because
with them on `errcheck` alone drops from 29 findings to 5, which is a pre-filtered view rather
than progress. `gofmt` and `go vet` are blocking too.

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
