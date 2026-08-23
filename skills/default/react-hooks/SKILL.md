---
name: react-hooks
description: The Rules of Hooks, the dependency array, and when an effect is the wrong tool entirely.
triggers: react, hook, useEffect, useState, useMemo, useCallback, component, jsx, tsx, exhaustive-deps
agents: worker, deep, corrector, reviewer, react-worker, react-tester, react-reviewer, ts-worker
paths: "**/*.tsx, **/*.jsx, **/hooks/**, web/**"
user-invocable: true
---

# React hooks: rules and dependency arrays

## The two hard rules

1. **Same hooks, same order, every render.** Never call a hook inside an `if`, a
   loop, a `try`, or after an early `return`. Put the condition INSIDE the hook.
2. **Only from a component or another hook.** Not from an event handler, not
   from a plain function.

```tsx
// wrong — the hook count changes between renders
if (!id) return null;
const [data, setData] = useState(null);

// right
const [data, setData] = useState(null);
if (!id) return null;
```

## The dependency array

`react-hooks/exhaustive-deps` is not a style rule. Every value the effect READS
from the component belongs in the array; omitting one gives a stale closure that
reads last render's value forever.

```tsx
useEffect(() => {
  const ctrl = new AbortController();
  fetchUser(userId, { signal: ctrl.signal }).then(setUser).catch(ignoreAbort);
  return () => ctrl.abort();          // cleanup — always, for anything ongoing
}, [userId]);                          // every read value listed
```

- **Never silence the lint with a comment.** If a dependency causes a loop, the
  fix is to remove the dependency (move the value into a ref, hoist the function
  out of the component, or use the `setState(prev => …)` updater form) — not to
  hide it.
- **Subscribe, listen, interval, timeout, observer ⇒ return a cleanup.** Without
  one, every re-render adds another.

## Effects you should not write

- **Deriving state.** `const full = `${first} ${last}`` during render beats a
  `useEffect` that sets `full`. Expensive? `useMemo`. Never an effect.
- **Syncing props into state.** Use the prop. If you need to reset on a prop
  change, give the component a `key` instead.
- **Fetching on an event.** That belongs in the handler, not in an effect
  watching a flag.

## The rest of the list

- `key={index}` breaks on reorder, insert and delete. Key by a stable id.
- State is immutable: `setItems([...items, x])`, never `items.push(x)`.
- `useCallback`/`useMemo` are only useful when the value crosses a `memo`
  boundary or is itself a dependency. Elsewhere they cost more than they save.
- A test that renders a component and asserts nothing covers nothing. Assert on
  what the user sees: `expect(screen.getByRole("button", {name: /save/i}))`.

Verify: `npx tsc --noEmit`, `npm run lint`, `npm test`.
