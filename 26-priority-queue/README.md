# Case 26 — Custom Work Queue (`NewQueue`)

## What this case covers

Every controller is driven by a work queue that holds the `reconcile.Request`s
waiting to be processed. Controller-runtime builds a sensible default queue for
you, but `controller.Options.NewQueue` lets you supply your own — most usefully
a priority queue that controls the *order* in which objects are reconciled.

## Key concepts

| Concept | Description |
|---|---|
| `controller.Options.NewQueue` | Factory `func(name, rateLimiter) TypedRateLimitingInterface` called once when the controller starts |
| Why a func, not a queue | Standard queues start a goroutine immediately; a factory avoids leaks when `controller.New` is called repeatedly |
| `TypedRateLimitingInterface[reconcile.Request]` | The queue interface the controller drives — `Add`, `Get`, `Done`, `AddRateLimited`, … |
| `NewTypedRateLimitingQueueWithConfig` | The default queue constructor you typically wrap rather than replace |

## How to run

```bash
# Requires a reachable cluster in ~/.kube/config
go run ./...
```

In another terminal, create ConfigMaps and watch the `enqueue` log lines from
the wrapping queue:

```bash
for i in 1 2 3; do kubectl create configmap cm-$i --from-literal=k=v -n default; done
```

## Concepts in depth

### Why NewQueue is a factory

The Kubernetes work queues start their own background goroutine the moment they
are constructed. If `controller.New` built the queue eagerly, calling it
repeatedly (as the builder may) would leak goroutines. Instead the controller
calls `NewQueue` **once**, lazily, right before it starts — passing the
controller name and the already-resolved `RateLimiter` so your queue honors the
same backoff behavior as the default.

### What a real priority queue would do

This example wraps the default queue and only *logs* each enqueue to keep the
wiring in focus. A production priority queue would override `Add`/`AddRateLimited`
to insert items by priority — for example, reconciling objects with spec changes
ahead of periodic resync events — so the controller drains the most important
work first under load. Upstream ships an experimental priority queue behind a
feature gate; `NewQueue` is the seam it plugs into.

> **Low-level primitive.** The default queue is correct for almost every
> controller. Only reach for `NewQueue` when reconcile *ordering* actually
> matters to correctness or latency.

## References

- [controller.Options.NewQueue godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller#Options)
- [workqueue.TypedRateLimitingInterface](https://pkg.go.dev/k8s.io/client-go/util/workqueue)
- [companion: case 25 — concurrency & rate limiting](../25-concurrency-and-ratelimit)
