---
name: api-versioning
description: HTTP API design and evolution — resource shapes, status codes, pagination, and how to change an API without breaking its clients.
triggers: api, rest, endpoint, http, versioning, openapi, breaking change, pagination, status code
agents: worker, architect, planner, reviewer, deep, docs
paths: "**/api/**, **/routes/**, **/handlers/**, **/controllers/**, **/openapi*.y*ml"
user-invocable: true
---

# API design and versioning

## Shape

- **Resources are plural nouns; the verb is the method.**
  `GET /orders/{id}`, `POST /orders`, `PATCH /orders/{id}` — never
  `POST /createOrder`.
- **`PUT` replaces, `PATCH` merges.** Pick one and honour it; a `PUT` that
  ignores absent fields silently corrupts data.
- **Every list endpoint is paginated from day one.** Cursor-based
  (`?limit=50&cursor=…`, returning `next_cursor`) survives inserts; offset
  pagination skips and repeats rows under concurrent writes.
- **Wrap collections in an object**, `{"items": [...], "next_cursor": "..."}`,
  never a bare top-level array — an array leaves nowhere to add metadata later.
- **Consistent field naming and time format.** Pick `snake_case` or `camelCase`
  once. Timestamps are RFC 3339 UTC strings; money is an integer of minor units
  plus a currency code, never a float.

## Status codes that carry information

`200` ok · `201` created (with a `Location` header) · `202` accepted (async) ·
`204` no content · `400` malformed · `401` unauthenticated · `403`
authenticated-but-forbidden · `404` not found · `409` conflict · `422`
semantically invalid · `429` rate limited (with `Retry-After`) · `5xx` our fault.

Errors have a stable machine-readable shape:
`{"error": {"code": "order_not_found", "message": "…", "details": {...}}}`.
Clients branch on `code`; `message` is for humans and may change.

## Changing an API

**Additive changes are safe. Everything else needs a version.**

Safe: adding an endpoint, adding an optional request field, adding a response
field (clients must ignore unknown fields — say so in the docs).

Breaking: removing or renaming a field, changing a type, making an optional
field required, changing a status code or an error `code`, tightening
validation, changing pagination defaults.

To ship a breaking change:
1. Add the new form alongside the old (`/v2/orders`, or a new field).
2. Mark the old one deprecated — in the docs, in a `Deprecation`/`Sunset`
   header, and in the release notes — with a date.
3. Measure who still calls it. Remove only after that reaches zero or the
   announced date passes.

Version in the path (`/v1/…`) unless the project already versions by header.
Do not version per-endpoint; clients cannot track that.

## Always

- Validate input at the boundary and reject unknown fields on writes.
- Make writes idempotent (an `Idempotency-Key` header, or a natural key) —
  clients retry.
- Never return an internal exception message or a stack trace to a client.
- The OpenAPI spec and the handler must be changed in the same commit.
