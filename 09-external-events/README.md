# Case 09: External Events via source.Channel

Inject reconcile requests from outside Kubernetes using a Go channel, enabling cron-style scheduling, webhook integration, and polling of external systems.

## What this case teaches

- `source.Channel(ch, handler)` — wire a plain Go channel as a controller watch source
- `event.GenericEvent{Object: ...}` — the synthetic event type for non-Kubernetes triggers
- `time.Ticker` goroutine pattern for periodic work independent of Kubernetes API events
- Buffered channels (`make(chan event.GenericEvent, 10)`) to decouple the producer from the controller's work queue
- `controller.New()` low-level API — building a controller without the `builder.For()` shorthand
- `handler.EnqueueRequestForObject{}` — extract a `NamespacedName` from a `GenericEvent` to form a `reconcile.Request`

## Key concepts

### Why source.Channel?

Every controller needs a trigger — something that tells it "go reconcile this object now." The standard triggers are Kubernetes API watches: a resource was created, updated, or deleted. But not every controller is purely reactive to Kubernetes state. Sometimes you need to:

- Poll an external database or API on a schedule and reconcile based on what you find
- Handle a webhook from an external system (GitHub, PagerDuty, etc.) that arrives over HTTP
- Run periodic background work on a fixed interval (a poor-man's CronJob at the controller level)

`source.Channel` fills this gap. It lets any Go goroutine send a `GenericEvent` into a channel, and controller-runtime translates each event into a `reconcile.Request` that flows through the normal work queue with all the usual rate limiting and deduplication.

### Why GenericEvent?

The four Kubernetes event types — `CreateEvent`, `UpdateEvent`, `DeleteEvent`, and `GenericEvent` — all carry an `Object` (a `client.Object`). `GenericEvent` is the odd one out: it does not correspond to a specific Kubernetes API server operation. It is a synthetic event you create yourself. The `Object` you embed is used by `handler.EnqueueRequestForObject` to extract the `namespace/name` key for the reconcile request. The object does not need to exist in the cluster; you are just using it as a carrier for the key.

### Why a buffered channel?

The ticker goroutine runs on its own schedule. The controller may be busy when the next tick fires. A buffered channel (size 10 here) prevents the ticker from blocking — the goroutine continues regardless of how fast the controller drains the channel. If the buffer fills up (e.g., the controller is down for an extended period), ticks are dropped rather than queuing unboundedly. This is the right trade-off for periodic work: a missed tick is fine; an unbounded queue is not.

### Why controller.New() instead of builder.For()?

`ctrl.NewControllerManagedBy(mgr).For(&SomeResource{})` is the high-level API and is perfect for watching a specific typed resource. `controller.New()` gives you a bare controller with no watches pre-wired. You add each watch explicitly with `c.Watch(...)`. Use this low-level API when:

- The controller does not naturally "own" a single primary resource
- You need to compose multiple heterogeneous watch sources
- You are injecting synthetic events (like here) and there is no real Kubernetes resource type to declare in `For()`

## How to run

```bash
cd 09-external-events
go run ./...
```

No CRD installation needed — this case watches no custom resources.

## What to observe

Every 5 seconds you will see two log lines:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="external event injected"
time=2024-01-15T10:23:01.235+01:00 level=INFO msg="tick reconcile" key=default/external-trigger
```

The first comes from the ticker goroutine confirming the event was sent. The second comes from `TickReconciler.Reconcile` when the controller processes that event. The `key` field is the `namespace/name` extracted from the synthetic `ConfigMap` object embedded in the `GenericEvent`.

Notice:
- The reconciler logs `default/external-trigger` — matching the `Name`/`Namespace` set on the synthetic object
- The interval is not perfectly 5.000s; the ticker fires at wall-clock intervals but the reconcile is subject to work-queue rate limiting
- There is no corresponding `ConfigMap` named `external-trigger` in the cluster — the reconciler would fail if it tried to `r.Get()` the object, because it does not exist. This case's reconciler just logs the key, demonstrating the decoupled nature of the event source

Stop with `Ctrl-C`.

## Key code walkthrough

**Buffered channel declaration** (`main.go:50`):
```go
ch := make(chan event.GenericEvent, 10)
```
Buffer size 10 prevents the ticker goroutine from blocking if the controller falls behind.

**Low-level controller construction** (`main.go:52-54`):
```go
c, err := controller.New("tick-controller", mgr, controller.Options{
    Reconciler: &TickReconciler{Client: mgr.GetClient()},
})
```
No `For()` call — watches are added manually below.

**Wiring the channel as a watch source** (`main.go:61`):
```go
c.Watch(source.Channel(ch, &handler.EnqueueRequestForObject{}))
```
`source.Channel` wraps the Go channel. The handler extracts the `namespace/name` from the `GenericEvent.Object` to produce a `reconcile.Request`.

**Ticker goroutine** (`main.go:66-79`):
```go
go func() {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    for range ticker.C {
        ch <- event.GenericEvent{
            Object: &corev1.ConfigMap{
                Name:      "external-trigger",
                Namespace: "default",
            },
        }
        ctrl.Log.Info("external event injected")
    }
}()
```
The goroutine is a pure Go producer — it knows nothing about controller-runtime internals. It just puts events into a channel.

## References

- [controller-runtime source godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/source)
- [controller-runtime handler godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/handler)
- [controller-runtime event godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/event)
- [upstream source/channel.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/source/source.go)
- [upstream handler/enqueue.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/handler/enqueue.go)
