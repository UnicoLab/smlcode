---
name: go-concurrency
description: Goroutine lifetime, context cancellation, channel direction and the mistakes that make Go concurrency leak or race.
triggers: goroutine, channel, sync.WaitGroup, mutex, context, concurrency, race, worker pool
agents: worker, deep, corrector, reviewer, go-worker, go-tester, go-reviewer
paths: "**/*.go"
user-invocable: true
---

# Go concurrency without leaks

**Every goroutine needs an owner who knows how it stops.** If you cannot point
at the line that ends it, it is a leak.

```go
func (s *Server) Start(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)   // or a WaitGroup + explicit done chan
	g.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()          // the exit the owner can rely on
			case job := <-s.jobs:
				if err := s.handle(ctx, job); err != nil {
					return err
				}
			}
		}
	})
	return g.Wait()
}
```

## The mistakes, in the order they actually happen

1. **A goroutine writing to a channel nobody reads.** The sender blocks forever
   and the goroutine never returns. Give the channel a buffer sized to the known
   number of sends, or select on `ctx.Done()` in the send.
2. **`wg.Add` inside the goroutine.** It races with `wg.Wait()`. Add before
   `go`, and `defer wg.Done()` as the goroutine's first line.
3. **Capturing the loop variable** (Go < 1.22): pass it as an argument.
4. **A `context.Context` stored in a struct.** Pass it as the first parameter.
   The one exception the standard library makes is a request-scoped struct that
   is itself short-lived.
5. **Ignoring the context in a blocking call.** `time.Sleep(d)` cannot be
   cancelled; `select { case <-time.After(d): case <-ctx.Done(): }` can.
6. **A mutex copied by value.** Once a struct has a `sync.Mutex`, it must be
   passed by pointer — `go vet` catches this; run it.
7. **`defer mu.Unlock()` missing on one return path.** Put the defer on the line
   after the Lock, always.
8. **Closing a channel from the receiver side.** Only the sender closes, and
   only once. A second close panics.
9. **Assuming `-race` proves safety.** It only reports races it observed; a
   single-goroutine test observes none. Exercise the concurrent path.

## Verifying

`go test ./... -race -count=1`. `-count=1` defeats the test cache; without it a
"fix" can pass on a cached result.
