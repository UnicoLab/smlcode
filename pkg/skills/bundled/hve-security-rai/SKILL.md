---
name: hve-security-rai
description: STRIDE threat modeling, OWASP/MCSB security planning, OpenSSF/SLSA supply chain hygiene, and NIST AI RMF Responsible AI.
triggers: stride, threat-model, owasp, sssc, slsa, sbom, rai, responsible-ai, prompt-injection, security-plan
agents: security-planner, sssc-planner, rai-planner, code-review-security-reviewer, security-reviewer, reviewer
user-invocable: true
---

# HVE Security, Software Supply Chain (SSSC) & Responsible AI (RAI)

## 1. STRIDE Threat Modeling (`security-planner`, `code-review-security-reviewer`)
Evaluate every trust boundary and external input against STRIDE:
- **S — Spoofing**: Authn token validation, origin checks, session integrity.
- **T — Tampering**: Path traversal guards, atomic file writes, schema validation, SQL/command parameterization.
- **R — Repudiation**: Structured audit events and tamper-evident run logs.
- **I — Information Disclosure**: Secret redaction, sanitized error responses, least-privilege file reads.
- **D — Denial of Service**: Bounded I/O (`io.LimitReader`), context timeouts, rate/size caps on payloads and tool outputs.
- **E — Elevation of Privilege**: Sandbox enforcement, path-scope containment (`HARD SCOPE`), deny-lists on sensitive directories.

## 2. Software Supply Chain Security (`sssc-planner`)
- Pin dependency versions and commit lockfiles (`go.sum`, `package-lock.json`).
- Minimize transitive dependencies; audit build scripts, CI workflows, and artifact provenance (OpenSSF / SLSA principles).

## 3. Responsible AI — NIST AI RMF 1.0 (`rai-planner`)
- **Map & Measure**: Identify model failure modes, hallucination risks, and untrusted repository content boundaries.
- **Manage & Govern**: Defuse prompt-injection markers in untrusted files, enforce grammar-locked schemas (`pkg/schema`), and require human-in-the-loop gates (`clarify`, `plan_approve`, `approve`) for high-impact actions.
