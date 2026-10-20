---
name: hve-requirements
description: BRD 3-phase discovery, PRD 7-phase specification, Epic/Story/Task decomposition, and Socratic ADR coaching.
triggers: brd, prd, adr, requirements, stakeholder, acceptance-criteria, user-story, backlog, c4, mermaid
agents: brd-builder-worker, prd-builder-worker, functional-planner, backlog-manager-worker, adr-creation-worker, arch-diagram-worker, clarifier, architect, planner
user-invocable: true
---

# HVE Requirements, Backlog & Architecture Decisions

## 1. BRD 3-Phase Framework (`brd-builder-worker`)
- **Discover**: Problem statement, business drivers, and stakeholder map (Mendelow Power × Interest grid).
- **Define**: SMART business objectives, measurable KPIs, in-scope vs. explicit out-of-scope boundaries.
- **Govern**: Risks, assumptions, dependencies (RAID), and sign-off gates.

## 2. PRD 7-Phase Specification (`prd-builder-worker`)
1. Problem & User Personas
2. User Journeys & UX Interaction States
3. Functional Requirements (prioritized Must / Should / Won't)
4. Data Contracts, APIs & Config Surface
5. Non-Functional Requirements (Latency, Reliability, Security, Accessibility)
6. Explicit Non-Goals
7. Testable Acceptance Criteria (`Given / When / Then` or verifiable commands)

## 3. Functional Decomposition (`functional-planner`, `backlog-manager-worker`)
- Break specifications into **Epic → Feature → User Story → Atomic Implementation Task**.
- Every task lists exact files in scope, dependencies, and verification criteria.

## 4. Socratic ADR & Architecture Diagrams (`adr-creation-worker`, `arch-diagram-worker`)
- **ADR Format**: Status, Context & Forces, Decision Drivers, Considered Options (with pros/cons), Decision Outcome, Positive/Negative Consequences.
- **Diagrams**: Ground Mermaid (`flowchart`, `sequenceDiagram`, `stateDiagram-v2`) in real package paths and data flows.
