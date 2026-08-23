---
name: dockerfile-hygiene
description: Dockerfiles that build fast, stay small and do not run as root — layer ordering, multi-stage builds and the cache rules behind them.
triggers: docker, dockerfile, container, image, compose, alpine, distroless, entrypoint
agents: worker, deep, corrector, reviewer, architect
paths: "**/Dockerfile*, **/docker-compose*.y*ml, **/.dockerignore"
user-invocable: true
---

# Dockerfile hygiene

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./          # deps first: this layer caches across code edits
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ./cmd/app

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
```

## Layer order is the build time

Copy the dependency manifest and install dependencies **before** copying the
source. `COPY . .` first invalidates every later layer on any code change, so
every build reinstalls everything.

## The rules

- **Multi-stage.** The final image should contain the artifact and its runtime,
  not the compiler, the package index, or the test fixtures.
- **Pin the base image** to a version (`python:3.12-slim`, not `python:latest`).
  `latest` makes a build reproducible only by accident.
- **`USER` a non-root account** before `ENTRYPOINT`. Root in a container is root
  on the host in more configurations than people expect.
- **`ENTRYPOINT` in exec form** (`["/app"]`). Shell form wraps the process in
  `/bin/sh -c`, which swallows `SIGTERM` — the container then takes the full
  kill timeout to stop on every deploy.
- **A `.dockerignore`** with at least `.git`, `node_modules`, build output and
  local env files. Without one the whole working tree is sent to the daemon and
  secrets land in layers.
- **Never `COPY` a secret**, even if a later layer deletes it — every layer is
  in the image. Use build secrets (`RUN --mount=type=secret`) or runtime env.
- **Combine `apt-get update` with the install** in one `RUN` and clean the lists
  in the same layer, or the cached index goes stale and the cleanup does nothing.
- **`HEALTHCHECK`** when an orchestrator will use it; leave it out when it won't.

## Verify

`docker build .` succeeding is not enough — check the final size
(`docker images`) and that the process runs as the expected user
(`docker run --rm <img> id`, when the image has a shell).
