# Release qualification — 2026-09-26–27

**Status: v0.28.0 published and verified.**
[PR #42](https://github.com/UnicoLab/smlcode/pull/42) merged after every CI check
passed. The [release workflow](https://github.com/UnicoLab/smlcode/actions/runs/36280848232)
passed its full gate and published [v0.28.0](https://github.com/UnicoLab/smlcode/releases/tag/v0.28.0)
from commit `fb8cddc`. All downloadable binaries and installers were then
checksum-verified, and the published macOS arm64 binary passed the Studio smoke.

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

Local checks ran on macOS arm64 with Go 1.27.0 and Node 24.21.0. Release CI
ran on Linux with the module-selected Go toolchain and Node 22. The workflow
stamps the final version, source commit and build time into every artifact.

| Check | Result |
| --- | --- |
| Complete local gate (`GOFLAGS=-p=4 make -j2 check`) | Passed; 72.2% coverage |
| Merged-tree CI and release gate | Passed, including package/integration race tests, lint, coverage and frontend checks; release-run coverage 71.2% against the 63% floor; 365 Studio tests |
| Studio browser checks | Passed on desktop and phone: layout, task navigation, motion preference persistence and 3D rendering; no page errors |
| Published macOS arm64 Studio | Downloaded release binary: all ten main pages navigate, authenticated API works and unauthenticated config access is denied; no browser exceptions or server errors |
| CLI configuration regression tests | Passed |
| Live delivery qualification guard regression tests | Passed; failed, unfinished, missing and timed-out results cannot qualify |
| Strict documentation build | Passed; internal broken links now fail the build |
| Documentation inventory | All 39 public help entries covered; runtime schema exposes 126 configuration fields |
| Version consistency and repository-reference checks | Passed |
| Go vulnerability scan | No known vulnerabilities found |
| Full npm dependency audit, including development dependencies | Zero reported vulnerabilities after updates |
| Native Laya Go-client smoke | Passed against upstream `laya-serve` 0.3.20, `multilingual`, CPU; temporary server stopped afterward |
| Live discovery, configure, chat and OpenAI-compatible decision smoke | Passed against oMLX with `Qwen3-Coder-30B-A3B-Instruct-MLX-4bit` |
| Release binaries | Six published macOS/Linux/Windows arm64/amd64 binaries; real Studio assets embedded; all downloaded checksums verified |
| Distribution metadata | All three installer checksums, four Homebrew binary hashes and both offline macOS bundles match the release |
| Binary identity | Linux amd64 smoke passed in CI; downloaded macOS arm64 reports v0.28.0, commit fb8cddc, build time and empty SourceRoot |
| Generated application, independently verified | Passed: Go race tests, current acceptance guard, real browser loading/refresh, 24 concurrent increments, HTTP/network failure states and no page exceptions |
| Live two-team application delivery | Passed: 3/3 tasks done, zero failed or unexecuted; correct frontend-static/backend-go teams; 771 seconds including setup |

Cross-compilation is not execution on all six platforms. Numeric model-response
smokes establish protocol compatibility, not predictive accuracy or a speedup.
The decision feature remains opt-in until evaluated on representative projects.
Go lint reports zero findings. Studio lint passes with 41 existing warnings and
zero errors; the warnings remain cleanup work.

The live suite ran before the final comment-only Node-file guard. Its retained
application then passed that newer guard independently. The generated JavaScript
checks validate source structure; the separate real-browser checks provide the
behavioral evidence listed above. Upstream integration changed Studio and release
metadata, and the merged Studio was rebuilt and checked again in Chromium.

## Resolved qualification blockers

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

One earlier attempt lost its local oMLX endpoint mid-run; it was restored and
that interrupted attempt was not counted as passing. The successful rerun used
the strict delivery guard, which requires the underlying result to report
success with no failed or unfinished tasks.

To repeat qualification against a deployment's actual provider/checkpoint:

```bash
SLMCODE_E2E_ARTIFACTS=/tmp/slm-release make e2e-release
```

Do not infer success from an earlier legacy PASS label, skip integration, or
treat an unavailable check as passing.

## Documentation coverage

The [feature index](features.md) maps public command groups and harness features
to their guides: CLI/TUI/Studio, tasks and sessions, teams and pipelines, agents
and skills, blocks/stacks/hooks/MCP, permissions, context and retrieval, memory,
evolution, metrics, graph, calibration, evaluation, autoresearch and providers.
[Migration notes](migration.md#release-preparation-updates) cover the CLI JSON
behavior and updated source-build prerequisites. Maintainers should follow the
[release checklist](https://github.com/UnicoLab/smlcode/blob/main/RELEASE.md).
