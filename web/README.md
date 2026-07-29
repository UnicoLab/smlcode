# Studio UI source

The production UI is embedded from `cmd/slmcode/ui` (React via CDN) so `go build`
needs no Node toolchain.

This `web/` folder is reserved for a future Vite + TypeScript build that can
replace the embedded assets (`npm run build` → copy into `cmd/slmcode/ui`).
