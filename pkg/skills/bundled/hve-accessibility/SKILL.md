---
name: hve-accessibility
description: WCAG 2.2 AA, ARIA Authoring Practices Guide (APG), Section 508, and EN 301 549 accessibility planning and auditing.
triggers: accessibility, a11y, wcag, aria, keyboard-navigation, screen-reader, focus-trap, contrast, section-508
agents: accessibility-planner, accessibility-reviewer, ux-architect, ux-worker, ux-reviewer, react-worker, web-worker
user-invocable: true
---

# HVE Accessibility (WCAG 2.2 AA & ARIA APG)

## 1. Surface Inventory & Coverage Matrix (`accessibility-planner`)
Before implementing or auditing UI changes, inventory every interactive surface:
- **Dialogs & Popovers**: `role="dialog"`, `aria-modal="true"`, accessible title, focus trap on open, focus restoration on close, `Escape` key dismissal.
- **Forms & Toggles**: Explicit `<label>` or `aria-label`, `aria-pressed` / `aria-checked` on toggle pills, `aria-invalid` and `aria-describedby` for inline errors.
- **Dynamic Live Regions**: Use `aria-live="polite"` or `role="status"` for streaming progress, gate notifications, and async feedback.

## 2. WCAG 2.2 AA Verification Checklist (`accessibility-reviewer`)
- **Perceivable**: Sufficient text/background contrast ($\ge 4.5:1$ normal text, $\ge 3:1$ UI components); never rely on color alone to convey status (pair color with icon or text badge).
- **Operable**: Full keyboard operability (`Tab`, `Shift+Tab`, `Enter`, `Space`, arrow keys in composite widgets), visible `:focus-visible` ring, and adequate target sizes.
- **Understandable**: Predictable mode switching, clear input labels, and actionable validation messages.
- **Robust**: Prefer native semantic HTML (`<button>`, `<select>`, `<optgroup>`, `<dialog>`, `<nav>`) over custom `<div>` click handlers.
