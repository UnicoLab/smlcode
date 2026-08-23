---
name: rust-errors
description: Rust error handling that compiles and reads well — thiserror for libraries, anyhow for binaries, `?` instead of unwrap.
triggers: rust, error, unwrap, expect, thiserror, anyhow, Result, panic, borrow checker, ?
agents: worker, deep, corrector, reviewer, rust-worker, rust-tester, rust-reviewer
paths: "**/*.rs, **/Cargo.toml"
user-invocable: true
---

# Rust error handling

**Library code returns errors. Binary code reports them. Neither unwraps.**

## A library: a concrete enum with `thiserror`

```rust
use thiserror::Error;

#[derive(Debug, Error)]
pub enum StoreError {
    #[error("key {0} not found")]
    NotFound(String),
    #[error("io failure")]
    Io(#[from] std::io::Error),      // `?` converts std::io::Error for free
}

pub fn load(path: &Path) -> Result<Config, StoreError> {
    let raw = std::fs::read_to_string(path)?;   // From<io::Error> does the work
    parse(&raw).ok_or_else(|| StoreError::NotFound(path.display().to_string()))
}
```

Callers can `match` on the variants. `Box<dyn Error>` across a public API takes
that away and is a downgrade, not a simplification.

## A binary: `anyhow` with context

```rust
use anyhow::{Context, Result};

fn main() -> Result<()> {
    let cfg = load(Path::new("app.toml"))
        .context("loading app.toml")?;          // context reads as a trace
    run(cfg)
}
```

## unwrap discipline

`unwrap()` / `expect()` are acceptable in exactly three places: a test, `main`,
and an invariant the surrounding code has just proved (say so in `expect`'s
message). Everywhere else use `?`, `ok_or_else`, `unwrap_or_default`, or
`if let Some(x)`.

`panic!`, `todo!()`, `unimplemented!()` in a code path the task was meant to
implement is an unfinished function, not an error strategy.

## Borrow checker, briefly

A borrow error is telling you two owners want the same value. Reach for, in
order: (1) narrow the borrow's scope with a block or an earlier `let`, (2) take
`&` instead of moving, (3) split the struct so the two fields borrow
independently, (4) `clone()` — when a copy is what you actually meant, not to
silence the compiler. `Rc<RefCell<T>>` is for genuine shared mutation and moves
the failure from compile time to run time; do not reach for it first.

## Async

Inside an `async fn`, a blocking call (`std::fs`, `std::thread::sleep`, a
`std::sync::Mutex` held across an `.await`) stalls the whole executor thread.
Use the runtime's equivalents (`tokio::fs`, `tokio::time::sleep`,
`tokio::sync::Mutex`).

Verify: `cargo check --quiet` while editing, then
`cargo clippy -- -D warnings` and `cargo test --quiet`.
