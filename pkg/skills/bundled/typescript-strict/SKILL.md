---
name: typescript-strict
description: Getting and staying tsc-strict-clean — narrowing over assertions, unknown over any, and the ESM/CJS import rules.
triggers: typescript, tsc, strict, any, unknown, tsconfig, type error, null, undefined, esm
agents: worker, deep, corrector, reviewer, ts-worker, ts-tester, ts-reviewer, react-worker
paths: "**/*.ts, **/*.tsx, **/*.mts, **/tsconfig*.json"
user-invocable: true
---

# TypeScript under `strict`

`"strict": true` turns on `strictNullChecks`, `noImplicitAny` and friends. It is
the setting that makes the type checker catch the bugs a small model writes.

## Never reach for the escape hatches

| Escape hatch | What to do instead |
|---|---|
| `any` | `unknown` + a narrow (`typeof`, `in`, a type guard, a zod parse) |
| `as Foo` | a type guard: `function isFoo(x: unknown): x is Foo` |
| `as unknown as Foo` | fix the type; this asserts two lies in a row |
| `foo!` | `if (!foo) return` / `foo?.bar` / `foo ?? fallback` |
| `@ts-ignore` | `@ts-expect-error` with a comment, only in tests, only briefly |

## Narrowing is the whole skill

```ts
type Result<T> = { ok: true; value: T } | { ok: false; error: string };

function render<T>(r: Result<T>) {
  if (!r.ok) return r.error;   // discriminant narrows both branches
  return String(r.value);      // r.value is T here, no assertion needed
}
```

- A **discriminated union** (a literal `kind`/`ok`/`type` field) narrows for free
  and makes `switch` exhaustive: give the default branch
  `const _never: never = x`, and adding a variant becomes a compile error.
- `Array.prototype.find` returns `T | undefined`. Handle it.
- `JSON.parse` returns `any`. Validate the shape (zod, a guard) at the boundary
  — that is the one place runtime checking earns its cost.
- Index access is `T | undefined` only under `noUncheckedIndexedAccess`; if the
  project has it on, respect it rather than turning it off.

## Async

- Every call to an `async` function needs `await` or an explicit `.catch`. A
  floating promise turns a thrown error into an unhandled rejection that no test
  catches. `no-floating-promises` in eslint finds them.
- `array.forEach(async …)` does not wait. Use `for (const x of xs) { await … }`
  for sequential, `await Promise.all(xs.map(f))` for parallel.

## Modules

- `"type": "module"` in package.json ⇒ ESM: relative imports need the **`.js`**
  extension even from a `.ts` source, and `__dirname`/`require` do not exist
  (use `import.meta.url`). No `"type"` ⇒ CommonJS, and the opposite holds.
- `import type { Foo }` for type-only imports: it is erased, so it cannot create
  a runtime cycle.

Verify: `npx tsc --noEmit` first, then lint, then tests. tsc prints nothing when
it is happy.
