---
name: sql-migrations
description: Schema migrations that do not take the site down — expand/contract, non-blocking index builds, and what never belongs in a migration.
triggers: migration, alembic, schema, alter table, ddl, flyway, liquibase, prisma migrate, sql
agents: worker, deep, corrector, reviewer, architect, planner
paths: "**/migrations/**, **/migrate/**, **/*.sql, **/alembic/**, **/db/**"
user-invocable: true
---

# Migration safety

**The rule:** old code and new code run at the same time during a deploy. Every
migration must be correct for both.

## Expand / contract

Never rename or drop in one step. Three deploys:

1. **Expand** — add the new nullable column / new table. Old code ignores it.
2. **Backfill + dual-write** — new code writes both, reads the new one with a
   fallback. Backfill in batches, not one `UPDATE` over the whole table.
3. **Contract** — once nothing reads the old column, drop it.

A `DROP COLUMN` in the same release as the code that stopped using it means any
instance still running the previous version starts erroring the moment the
migration lands.

## Locks — the operations that stop the world

- `ALTER TABLE … ADD COLUMN … NOT NULL DEFAULT <value>` rewrites the whole table
  on older engines. Add it nullable, backfill, then add the constraint.
- `CREATE INDEX` takes a write lock. Postgres: `CREATE INDEX CONCURRENTLY`
  (which cannot run inside a transaction — most tools need an explicit opt-out).
  MySQL: `ALGORITHM=INPLACE, LOCK=NONE`.
- Adding a foreign key validates every existing row. Postgres: add `NOT VALID`,
  then `VALIDATE CONSTRAINT` separately.
- A long migration inside one transaction holds its locks for the whole run.
  Batch it: `WHERE id BETWEEN … ` in a loop, with a sleep between batches.

## Always

- **Every migration has a tested down/rollback**, or an explicit note saying it
  is irreversible and why.
- **Migrations are append-only.** Editing one that has run anywhere makes the
  checksum diverge; write a new one.
- **No data-dependent logic in DDL files.** Backfills that need application
  logic belong in a script the migration triggers, not in raw SQL that duplicates
  business rules.
- **Name the file for what it does**: `20240612_add_orders_status_index.sql`.
- Run it against a **copy of production-sized data** before believing the timing.
