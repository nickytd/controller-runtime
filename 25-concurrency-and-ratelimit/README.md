# Case 25 — Concurrency & Rate Limiting

## What this case covers

By default a controller reconciles one object at a time and backs off failing
items with an exponential rate limiter. Both are tunable through
`controller.Options` on the builder — this case sets both explicitly.

## Key concepts

| Concept | Description |
|---|---|
| `MaxConcurrentReconciles` | Max reconciles running in parallel (default `1`) |
| `.WithOptions(controller.Options{...})` | Passes controller options through the builder |
| `RateLimiter` | `workqueue.TypedRateLimiter[reconcile.Request]` controlling requeue timing |
| `NewTypedItemExponentialFailureRateLimiter` | Per-item exponential backoff between `baseDelay` and `maxDelay` |

## How to run

```bash
# Requires a reachable cluster in ~/.kube/config
go run ./...
```

Create several ConfigMaps quickly and watch up to three reconciles interleave
in the logs:

```bash
for i in 1 2 3 4 5; do kubectl create configmap cm-$i --from-literal=k=v -n default; done
```

## Concepts in depth

### MaxConcurrentReconciles

Raising this increases throughput when reconciles are I/O-bound (API calls),
at the cost of more concurrent load on the API server. Reconcilers must already
be safe to run concurrently for *different* objects — controller-runtime
guarantees a single object key is never reconciled by two workers at once, so
you only need per-object idempotency, not global locking.

### Relationship to the client-go companion

The client-go rate-limiting case builds the workqueue and rate limiter by hand.
controller-runtime owns the queue for you and exposes the same knobs as
`controller.Options` — `RateLimiter` here is the identical
`workqueue.TypedRateLimiter` type, just injected rather than constructed in your
own run loop. Note that supplying a custom `RateLimiter` **replaces** the entire
default `MaxOfRateLimiter` (which combines per-item exponential backoff with an
overall token-bucket limiter) — it does not stack on top of it.

## References

- [controller.Options godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller#Options)
- [Builder.WithOptions](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/builder#Builder.WithOptions)
- [workqueue rate limiters](https://pkg.go.dev/k8s.io/client-go/util/workqueue)
- [companion: client-go 20-rate-limiting](../../client-go/20-rate-limiting)
