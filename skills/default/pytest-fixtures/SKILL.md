---
name: pytest-fixtures
description: pytest fixtures, parametrize, tmp_path and monkeypatch — the idioms that replace setUp boilerplate and shared mutable state.
triggers: pytest, fixture, parametrize, conftest, test_, unittest, mock, monkeypatch
agents: worker, tester, deep, corrector, python-worker, python-tester, python-reviewer
paths: "**/test_*.py, **/*_test.py, **/conftest.py, **/*.py"
user-invocable: true
---

# pytest fixtures and parametrize

## Parametrize instead of looping

```python
import pytest
from app.parse import parse, ParseError

@pytest.mark.parametrize(
    ("raw", "expected"),
    [
        pytest.param("a=1", {"a": 1}, id="single"),
        pytest.param("a=1,b=2", {"a": 1, "b": 2}, id="multiple"),
    ],
)
def test_parse_ok(raw, expected):
    assert parse(raw) == expected

def test_parse_rejects_empty():
    with pytest.raises(ParseError, match="empty input"):
        parse("")
```

A `for` loop inside one test stops at the first failure and reports one result;
`parametrize` reports each case separately and names it in the output.

## Fixtures

```python
@pytest.fixture
def store(tmp_path):                  # tmp_path is per-test and auto-removed
    s = Store(tmp_path / "db.sqlite")
    yield s                           # yield, not return, when teardown is needed
    s.close()
```

- **Scope is the whole contract.** The default `function` scope re-runs per test;
  `session`/`module` scope makes tests share state — only for genuinely
  read-only or expensive setup, and never for anything a test mutates.
- **`conftest.py`** holds fixtures shared across a directory. No import needed;
  no import is possible either — fixtures are resolved by name.
- **`monkeypatch`** for environment and attributes; it undoes itself:
  `monkeypatch.setenv("API_KEY", "x")`, `monkeypatch.setattr(mod, "now", fake)`.
  Setting `os.environ` directly leaks into every later test.
- **`tmp_path`** (a `pathlib.Path`) for files. Never write into the repo.

## Facts that decide pass/fail

- pytest only collects `test_*.py` / `*_test.py` files and `test_*` functions in
  them. A perfectly good test in `helpers.py` never runs.
- Exit code **5 means "no tests ran"** — that is a failure of the task, not a
  pass. Check the collected count.
- An `ImportError` during collection is reported as an ERROR, not a failure, and
  usually means a missing `__init__.py` or an uninstalled package.
- `python -m pytest` puts the current directory on `sys.path`; a bare `pytest`
  does not. That is the difference behind most "works locally" import failures.
- Mock where the name is **used**, not where it is defined:
  `monkeypatch.setattr("app.service.requests.get", fake)`.
