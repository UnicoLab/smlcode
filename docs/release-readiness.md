# Release qualification — 2026-09-26–27

**Status: final qualification in progress.** Earlier live attempts exposed
release blockers, now addressed below. A fresh complete delivery and final gate
must pass before this record can qualify the release. No release was published
during this audit.

## Implemented and hardened

- External decision providers: native Laya and OpenAI-compatible JSON chat, with
  optional knowledge reranking and navigation/planning/review/repair guidance.
  See [configuration, boundaries and fallbacks](laya.md). Hosting and training
  remain external; no Python model scripts or weights are bundled.
- `configure --json` now persists before claiming success and returns nonzero
  for failed discovery. Regression tests cover both behaviors.
- Live release checks exercise actual chat completion and JSON prediction.
  The squads check now rejects failed, unfinished and timed-out deliveries;
  structural team checks alone no longer produce a false green result.
- Vulnerability scanner installation works when Go's binary directory is outside
  `PATH`. Dependency freshness includes the npm lockfile.
- Studio's router and affected transitive dependencies are patched. Functional
  tests use bounded workers and scheduling headroom without removing assertions.
- A static frontend team supplies framework-free Node behavior tests. Planner
  and splitter prompts include the frozen API contract and required acceptance
  commands, so earlier architecture suggestions cannot silently replace its routes.
- Studio shows board-based delivery progress, delayed-connection feedback,
  readable mobile team shortcuts and a persistent Motion/Still setting. Phase
  scrolling stays inside its strip. Live insights no longer warn that an ongoing
  run lacks its final event.

## Verification record

This audit ran on macOS arm64 with Go 1.27.0 and Node 24.21.0. The source's
release version remains 0.26.0; changing it, committing, tagging and publishing
are separate release operations.

| Check | Result |
| --- | --- |
| Complete gate (`GOFLAGS=-p=4 make -j2 check`) | Passed with 72.2% coverage and 342 Studio tests; final role-finalization changes are being rechecked |
| Studio browser checks | Passed on desktop and phone: layout, task navigation, motion preference persistence and 3D rendering; no page errors |
| Packaged Studio | Real binary: all ten main pages navigate, authenticated API works and unauthenticated config access is denied; no browser exceptions or server errors |
| CLI configuration regression tests | Passed |
| Live delivery qualification guard regression tests | Passed; failed, unfinished, missing and timed-out results cannot qualify |
| Strict documentation build | Passed; internal broken links now fail the build |
| Documentation inventory | All 39 public help entries covered; runtime schema exposes 126 configuration fields |
| Version consistency and repository-reference checks | Passed |
| Go vulnerability scan | No known vulnerabilities found |
| Full npm dependency audit, including development dependencies | Zero reported vulnerabilities after updates |
| Native Laya Go-client smoke | Passed against upstream `laya-serve` 0.3.20, `multilingual`, CPU; temporary server stopped afterward |
| Live discovery, configure, chat and OpenAI-compatible decision smoke | Passed against oMLX with `Qwen3-Coder-30B-A3B-Instruct-MLX-4bit` |
| Release binary cross-compilation | macOS, Linux and Windows; arm64 and amd64; real Studio assets embedded |
| Live two-team application delivery | Passed: real-model two-team delivery completed in 771 seconds; independent artifact checks and integration with current main are pending |

Cross-compilation is not execution on all six platforms. Numeric model-response
smokes establish protocol compatibility, not predictive accuracy or a speedup.
The decision feature remains opt-in until evaluated on representative projects.
Go lint reports zero findings. Studio lint passes with 41 existing warnings and
zero errors; the warnings remain cleanup work.

## Qualification history

The live scenario asked for a Go counter endpoint and a plain HTML/JavaScript
client, with no npm or framework. It activated `backend-go` and `frontend-react`
teams. Implementation tasks completed, but integration verification did not:
the run reported tester/QA rejection and an unresolved defect after about
25 minutes. The old test printed PASS because it only checked structural
invariants; its underlying `success=false` result is the qualification evidence.
The test has now been corrected so that outcome cannot be accepted.

The team-library mismatch is now addressed by `frontend-static`, whose checks
use Node's built-in test runner without npm. Follow-up inspection also found
that planning omitted the frozen contract and invented `/counter` while the
manager had specified `/api/counter`. Both planner and splitter now receive
that contract and its acceptance commands. Regression tests cover selection,
contract propagation, token bounds and the Node test runner's shell restrictions.

Follow-up runs also exposed a separate acceptance allowlist that rejected Node
tests, a Node 24 zero-test exit that looked successful, and Go-only prompts on
JavaScript tasks. Regression tests now exercise real passing, failing and absent
Node suites, guard against unsafe flags, and keep task-specific verification
consistent across workers, reviewers and correctors. The static team uses
standard-library browser fakes instead of uninstalled third-party dependencies.

Further live verification exposed worker-only finalization instructions in the
underlying agent library. SLMCode now replaces that exact generated tail with
the active role schema before sending it to the provider. A regression test
executes the real ReAct dependency and inspects the outgoing finalization
request. Shared recovery/budget prompts also retain role contracts; tester
success is not synthesized from file writes. Review and correction preserve
the team charter, and a new dispatch can read files even if its predecessor
exhausted its repetition guard.

A follow-up attempt lost its local oMLX endpoint mid-run. The server was restored
and that interrupted run was not counted as a passing qualification. A fresh
complete run is required against the updated source:

```bash
make e2e-release ARGS='-run TestLiveReleaseSurface/squads'
```

Run the complete `make e2e-release` suite against the intended release
provider/checkpoint. Do not infer success from an earlier legacy PASS label,
skip integration, or treat an unavailable check as passing. Retain diagnostics
with `SLMCODE_E2E_ARTIFACTS=/tmp/slm-release` when investigating a failure.

## Documentation coverage

The [feature index](features.md) maps public command groups and harness features
to their guides: CLI/TUI/Studio, tasks and sessions, teams and pipelines, agents
and skills, blocks/stacks/hooks/MCP, permissions, context and retrieval, memory,
evolution, metrics, graph, calibration, evaluation, autoresearch and providers.
[Migration notes](migration.md#release-preparation-updates) cover the CLI JSON
behavior and updated source-build prerequisites. Maintainers should follow the
[release checklist](https://github.com/UnicoLab/smlcode/blob/main/RELEASE.md).
