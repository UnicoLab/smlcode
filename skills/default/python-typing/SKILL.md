---
name: python-typing
description: Type hints that actually help — Optional, generics, Protocol, and the mutable-default and import-cycle traps around them.
triggers: type hint, mypy, typing, Optional, dataclass, pyright, annotations, Protocol
agents: worker, deep, corrector, reviewer, python-worker, python-tester, python-reviewer
paths: "**/*.py, **/*.pyi"
user-invocable: true
---

# Python typing that pays for itself

```python
from __future__ import annotations          # `list[int]` etc. on any 3.x

from dataclasses import dataclass, field
from typing import Protocol, Sequence

@dataclass(frozen=True, slots=True)
class Order:
    id: int
    items: list[str] = field(default_factory=list)   # NOT `= []`

class Repo(Protocol):                        # structural: no base class needed
    def get(self, order_id: int) -> Order | None: ...

def total(orders: Sequence[Order]) -> float: ...      # accept, do not require list
```

## The traps

- **Mutable default arguments.** `def f(x=[])` / `={}` / `=set()` creates ONE
  object shared by every call. Use `=None` and build the default in the body.
  In a dataclass the equivalent is `field(default_factory=list)`.
- **`Optional` is not optional.** A function that can return `None` is
  `-> Foo | None`. Annotating it `-> Foo` makes the type checker bless a
  `NoneType has no attribute` crash.
- **Narrow before use.** `if x is None: return` at the top beats `x.y` guarded by
  nothing; the checker follows the early return.
- **Accept the widest type you can use, return the narrowest you have.**
  Parameters: `Sequence`, `Iterable`, `Mapping`. Returns: the concrete `list`.
- **`Any` disables checking** for everything it touches. `object` plus an
  `isinstance` narrow is almost always what was meant.
- **Import cycles.** Types needed only for annotations go under
  `if TYPE_CHECKING:` with `from __future__ import annotations` — that breaks the
  cycle without a function-local import. If you must import inside a function,
  leave a comment saying which cycle it breaks.
- **`==` vs `is`.** `is` for `None`, `True`, `False` and sentinels only.
- **Logging.** `logging.info("saved %s", oid)` — never an f-string; the f-string
  formats even when the level is off and destroys structured logging.

## Verify

`mypy .` or `pyright`, then `ruff check .`. A type annotation the checker never
ran over is a comment.
