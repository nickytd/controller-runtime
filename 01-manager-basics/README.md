# Case 01: Manager Basics

The Manager is the root object in controller-runtime — it owns the cache, client, scheme, and lifecycle of every controller and runnable in your operator.

## What this case teaches

- How to create a Manager with `manager.New()` and configure it via `manager.Options`
- How to disable the metrics and health-probe HTTP servers (useful for local experiments)
- How to set the global logger with `ctrl.SetLogger` and a slog-backed `logr` logger
- How to implement the `manager.Runnable` interface (`Start(ctx context.Context) error`)
- How to register an arbitrary background goroutine via `mgr.Add()`
- How `mgr.Start(signals.SetupSignalHandler())` ties the entire process to OS signals for graceful shutdown

## Key concepts

### Why does the Manager exist?

A Kubernetes controller needs several shared resources: a REST config to talk to the API server, an informer cache so controllers don't spam the API server with individual reads, a scheme that maps Go types to GVKs, a leader-election lock, metrics, health endpoints, and a way to coordinate shutdown. The Manager owns all of these and provides them to every controller registered with it. Without a Manager you would have to wire all of this yourself, which is exactly what controller-runtime is designed to avoid.

### Why use `signals.SetupSignalHandler()`?

This returns a `context.Context` that is cancelled when the process receives `SIGTERM` or `SIGINT`. Passing that context to `mgr.Start()` means every controller, every cache goroutine, and every custom `Runnable` receives the same cancellation signal and can shut down cleanly. This is the standard pattern for all production operators.

### Why implement `Runnable` instead of spawning a goroutine?

`mgr.Add(runnable)` gives the Manager control over your background work. The Manager starts it only after the cache has synced, stops it when the context is cancelled, and can propagate errors back through `mgr.Start`. A raw `go func()` bypasses all of that coordination.

## How to run

You need a reachable cluster (or a kubeconfig pointing at one — kind, k3d, or a real cluster all work).

```bash
cd 01-manager-basics
go run ./...
```

Stop with `Ctrl+C`.

## What to observe

When the manager starts you will see structured log lines from the slog text handler:

```
time=... level=INFO msg="ticker started" logger=ticker
```

When you press `Ctrl+C`:

```
time=... level=INFO msg="ticker stopped" logger=ticker
```

The key thing to notice is that the `ticker` stops cleanly after the signal — it does not get killed mid-operation. This is the graceful-shutdown guarantee the Manager provides.

## Key code walkthrough

**`manager.Options` — disabling side-servers** (`main.go:49-53`):
```go
mgr, err := manager.New(cfg, manager.Options{
    Metrics:                metricsserver.Options{BindAddress: "0"},
    HealthProbeBindAddress: "0",
})
```
`"0"` is not a valid port — it tells the server not to bind at all. In production you would set real addresses and add readiness/liveness endpoints.

**`ticker.Start` — the Runnable contract** (`main.go:30-36`):
```go
func (t *ticker) Start(ctx context.Context) error {
    logger := log.FromContext(ctx).WithName("ticker")
    logger.Info("ticker started")
    <-ctx.Done()
    logger.Info("ticker stopped")
    return nil
}
```
The contract is simple: block until `ctx.Done()` is closed, then return. The Manager calls `Start` in a goroutine and expects it to return when the context is cancelled.

**`mgr.Add` — registering the Runnable** (`main.go:60-63`):
The Manager queues all added Runnables and starts them only after the informer cache has performed its initial List+Watch sync. This ensures your background work never runs against a cold cache.

## References

- [controller-runtime Manager godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager)
- [upstream source: pkg/manager/manager.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/manager/manager.go)
- [upstream source: pkg/manager/runnable_group.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/manager/runnable_group.go)
- [upstream source: pkg/manager/signals/signal.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/manager/signals/signal.go)
- [controller-runtime book: Manager](https://book.kubebuilder.io/architecture)
