---
name: hve-ux-design
description: Design Thinking (DFV) and UX architecture for user flows, component hierarchy, and interactive state matrices.
triggers: ux, ui, design-thinking, wireframe, user-flow, interaction, state-matrix, empty-state, loading-state, modal, layout
agents: dt-coach-architect, dt-tutor-worker, ux-architect, ux-worker, ux-reviewer, clarifier, architect, react-worker, ts-worker, web-worker
user-invocable: true
---

# HVE Design Thinking & UX Architecture

## 1. Design Thinking Spaces & DFV Lens (`dt-coach-architect`, `dt-tutor-worker`)
- **Problem Space**: Clarify target persona, core job-to-be-done, pain points, and entry triggers before jumping to UI widgets.
- **Solution Space**: Propose 1–3 concrete interaction patterns using **Propose-and-Refine** (recommend a sensible default and highlight trade-offs).
- **DFV Evaluation**: Balance **Desirability** (user clarity & low cognitive load), **Feasibility** (fits existing component tree & state management), and **Viability** (aligned with product scope).

## 2. Interactive State Matrix (`ux-architect`, `ux-worker`, `ux-reviewer`)
Every interactive view, form, modal, or control MUST define all 5 states:
1. **Default / Idle**: Clear hierarchy, primary vs. secondary affordances, progressive disclosure of advanced options.
2. **Loading / Pending**: Disabled duplicate submission, inline spinner or skeleton, responsive feedback.
3. **Empty / Zero-Data**: Actionable empty state explaining what lives here and offering a 1-click starter action.
4. **Error / Validation**: Inline, human-readable error message with recovery action (never silent failures).
5. **Success / Confirmation**: Clear visual state transition or status indicator.

## 3. Ergonomics & Handoff Hygiene
- Reuse existing CSS variables, design tokens, and layout primitives before introducing new styles.
- Preserve keyboard navigation (`Tab`, `Enter`, `Escape` to dismiss modals) and responsive wrapping.
