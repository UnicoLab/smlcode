# Vendored fonts (optional)

Studio ships with **no webfont download**. Typography falls back to the
platform UI stack, which is correct for an offline-first local tool and costs
zero network requests.

To use Inter / JetBrains Mono instead, drop these two files here:

| File | Source |
|------|--------|
| `inter-variable.woff2` | https://github.com/rsms/inter/releases (`InterVariable.woff2`) |
| `jetbrains-mono-variable.woff2` | https://github.com/JetBrains/JetBrainsMono/releases (`JetBrainsMono[wght].woff2`, converted to woff2) |

`src/styles/fonts.css` already declares the matching `@font-face` rules with
`font-display: swap` and a `local()` first entry, so:

* if the files are present, they are used;
* if the user already has the font installed, the local copy wins and nothing
  is fetched;
* if neither is true, the `src` list fails and the system stack applies — no
  hang, no layout shift beyond the swap.

Vite copies everything under `public/` verbatim into `dist/`, which is then
embedded into the Go binary, so vendored fonts are served from the same
loopback origin as the rest of Studio.
