# Case 02: First Reconciler

A Reconciler is the function at the heart of every Kubernetes controller — it is called whenever something might have changed and it brings the world to the desired state.

## What this case teaches

- How to implement the `reconcile.Reconciler` interface (`Reconcile(ctx, Request) (Result, error)`)
- How to wire a reconciler to a resource type using the `ctrl.Builder` fluent API (`.For().Complete()`)
- Why a `reconcile.Request` carries only `Name` and `Namespace`, not the event or the object itself
- How to obtain a structured, context-aware logger with `ctrl.LoggerFrom(ctx)`
- The mental model of level-triggered reconciliation vs edge-triggered event callbacks

## Key concepts

### Why is the Reconciler loop level-triggered?

Most event systems (pub/sub, callbacks) give you the delta: "field X changed from A to B." The Kubernetes reconciler model deliberately discards that information. Your `Reconcile` function receives a name and namespace, then it is your job to fetch the current state from the API server and compute what needs to change.

This is called *level-triggered* control (analogous to hardware interrupts). The advantages are:
1. **Robustness**: if your process crashed and missed events, the next reconcile still sees the full current state.
2. **Idempotency**: running `Reconcile` ten times for the same object must produce the same result as running it once.
3. **Simplicity**: you never need to reconstruct current state from a chain of deltas.

### Why does Request not include the object?

Including the object in the Request would tie the reconcile logic to a point-in-time snapshot. By the time your code runs, the object may have been updated again. Fetching inside `Reconcile` ensures you always work with the latest version, which avoids stale-write conflicts.

### Why use `ctrl.LoggerFrom(ctx)` instead of a package-level logger?

The Manager injects a logger into the context that is already enriched with the controller name, the reconcile key (`name`/`namespace`), and a correlation ID. `ctrl.LoggerFrom(ctx)` retrieves that pre-enriched logger. Using a package-level logger loses all of that context and makes log correlation in production much harder.

### What does `ctrl.NewControllerManagedBy(mgr).For(&corev1.ConfigMap{}).Complete(r)` actually do?

- `.For(&corev1.ConfigMap{})` tells the builder to set up an informer on ConfigMaps, register event handlers, and enqueue a `Request` for each event.
- `.Complete(r)` builds the controller and registers it with the Manager. The controller is started when `mgr.Start()` is called.
- The builder is a convenience wrapper around the lower-level `controller.New` + `c.Watch` calls shown in case 08.

## How to run

```bash
cd 02-first-reconciler
go run ./...
```

In another terminal, trigger some events:

```bash
# Create a ConfigMap — triggers a reconcile
kubectl create configmap test-cm --from-literal=key=value -n default

# Edit it — triggers another reconcile
kubectl annotate configmap test-cm env=dev -n default

# Delete it — triggers a final reconcile (object will be gone when you fetch it)
kubectl delete configmap test-cm -n default
```

## What to observe

Each kubectl operation above triggers one or more log lines like:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="reconciling ConfigMap" controller=configmap controllerKind=ConfigMap ConfigMap="default/test-cm" namespace=default name=test-cm reconcileID="..." 
```

Notice:
- The `controller`, `controllerKind`, `namespace`, `name`, and `reconcileID` fields are injected automatically by the Manager's logger (via `ctrl.LoggerFrom(ctx)`); the reconciler itself only adds `msg`, plus the `name`/`namespace` it logs explicitly.
- The `name` and `namespace` values come from the `Request`, not from fetching the object.
- A single `kubectl annotate` may produce more than one reconcile call — the informer coalesces rapid events, but the controller sees every change that reaches it. This is expected and your code must be idempotent.
- After `kubectl delete`, the log fires once more. In a real controller you would fetch the object and get a "not found" error — that is the signal that deletion has occurred.

## Key code walkthrough

**The reconciler struct** (`main.go:25`):
```go
type ConfigMapReconciler struct{}
```
The struct holds any dependencies your reconciler needs — typically `client.Client`, `*runtime.Scheme`, a logger, etc. This one has none because it does not fetch the object yet.

**The Reconcile method signature** (`main.go:27`):
```go
func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error)
```
This is the entire `reconcile.Reconciler` interface. The `ctx` carries the logger and a deadline. `req.NamespacedName` is the only information passed in — everything else must be fetched.

**Wiring to ConfigMaps** (`main.go:55-57`):
```go
ctrl.NewControllerManagedBy(mgr).
    For(&corev1.ConfigMap{}).
    Complete(&ConfigMapReconciler{})
```
The type passed to `.For()` determines which resource the controller watches. Any create, update, or delete event on a ConfigMap enqueues a `Request` for that ConfigMap's name+namespace.

## References

- [reconcile.Reconciler godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile#Reconciler)
- [upstream source: pkg/reconcile/reconcile.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/reconcile/reconcile.go)
- [upstream source: pkg/builder/controller.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/builder/controller.go)
- [controller-runtime book: Reconciler](https://book.kubebuilder.io/cronjob-tutorial/controller-implementation)
- [Kubernetes control loop design](https://kubernetes.io/docs/concepts/architecture/controller/)
