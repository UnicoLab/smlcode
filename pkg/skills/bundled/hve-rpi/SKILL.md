---
name: hve-rpi
description: Hypervelocity Engineering RPI loop (Research, Challenge, Plan, Implement, Review) with FAR rubric and uncertainty tracking.
triggers: rpi, rpir, far-rubric, research, challenge, plan-validator, task-researcher, task-planner, task-implementer, task-reviewer
agents: task-researcher-explorer, rpi-challenger-reviewer, task-planner, plan-validator-reviewer, task-implementer-worker, task-reviewer, explorer, planner, reviewer
user-invocable: true
---

# HVE RPI Loop (Research → Challenge → Plan → Implement → Review)

## 1. Research with the FAR Rubric (`task-researcher-explorer`)
Score every finding before including it:
- **F — Factual**: Verified directly in the repository with `file:line` references. Never guess symbol names or signatures.
- **A — Actionable**: Identifies exact entry points, interfaces, callers, tests, and blast radius.
- **R — Relevant**: Directly constrained to the user's goal and `HARD SCOPE`.
- **Uncertainty Discipline**: Tag any unverified external assumption as `[Uncertain]` and state how to verify it.

## 2. Adversarial Challenge (`rpi-challenger-reviewer`)
- Challenge hidden assumptions, missing error paths, concurrency hazards, and scope creep before code is written.
- Flag any step that touches files outside the minimal touch list.

## 3. Atomic Planning & Validation (`task-planner`, `plan-validator-reviewer`)
- Order steps by dependency: types/interfaces → core logic → wiring → tests.
- Every step must name concrete target files and a verifiable completion check.
- `plan-validator-reviewer` rejects plans with vague steps, missing test coverage, or unrequested refactors.

## 4. Phase-Gated Implementation & Review (`task-implementer-worker`, `task-reviewer`)
- Implement one atomic step at a time strictly inside `HARD SCOPE`.
- Verify every diff against locked PRD criteria, UX interaction states, and quality gates.
