# Case 11: Structured Finalizers

Register named cleanup handlers using controller-runtime's `finalizer` package so that resources are never deleted before their external side effects are cleaned up.

## What this case teaches

- `finalizer.NewFinalizers()` and `.Register(name, impl)` — declare named cleanup handlers at startup
- The `finalizer.Finalizer` interface — implement `Finalize(ctx, obj) (Result, error)` for cleanup logic
- `r.finalizers.Finalize(ctx, widget)` — the single call that handles both registration and cleanup automatically
- `result.Updated == true` means the finalizer string on the object changed — you must persist it with `r.Update(ctx, widget)`
- `DeletionTimestamp` — the Kubernetes mechanism that gates deletion behind cleanup
- Why the structured approach beats manually manipulating `metadata.finalizers` strings

## Key concepts

### Why do finalizers exist?

When you call `kubectl delete widget my-widget`, Kubernetes does not immediately remove the object from etcd. Instead it sets `metadata.deletionTimestamp` to the current time and writes the object back. The object is now in a "terminating" state. It stays there until `metadata.finalizers` is empty. Only then does the API server perform the actual deletion.

This gives controllers a window to run cleanup before the object disappears: release a DNS entry, delete an S3 bucket, revoke credentials, notify an external service. Without finalizers, the object would be gone before your controller had a chance to act.

### The manual approach (and why it is fragile)

A naive reconciler manually manages the finalizer string:

```go
const myFinalizer = "example.com/cleanup"

// On a live object, ensure the string is present
if !controllerutil.ContainsFinalizer(obj, myFinalizer) {
    controllerutil.AddFinalizer(obj, myFinalizer)
    r.Update(ctx, obj)
    return
}

// On a deleting object, run cleanup then remove the string
if !obj.DeletionTimestamp.IsZero() {
    doCleanup(ctx, obj)
    controllerutil.RemoveFinalizer(obj, myFinalizer)
    r.Update(ctx, obj)
    return
}
```

This works but scatters the "is it deleting?" check and the update logic across the reconciler. It becomes unwieldy when you have multiple finalizers, each owned by a different subsystem.

### The structured approach

`pkg/finalizer` encapsulates this pattern behind a registry. You register named handlers once at startup:

```go
finalizers := finalizer.NewFinalizers()
finalizers.Register("example.com/widget-cleanup", &widgetCleanup{})
```

Then in the reconciler, one call handles everything:

```go
result, err := r.finalizers.Finalize(ctx, widget)
if result.Updated {
    r.Update(ctx, widget)
}
```

Internally, `Finalize` does:
1. If `DeletionTimestamp` is zero → add any missing finalizer strings to `metadata.finalizers` and set `result.Updated = true`
2. If `DeletionTimestamp` is set → call each registered `Finalizer.Finalize()` handler, then remove the finalizer string and set `result.Updated = true`

Your `widgetCleanup.Finalize` method only contains the cleanup logic — no string manipulation, no timestamp checking.

### Why the result.Updated check matters

`Finalize` modifies the in-memory object (adding or removing the finalizer string) but does not call the API server. That is intentional — the caller decides when to write. If `result.Updated` is true, you must call `r.Update(ctx, widget)` to persist the change. Forgetting this means the finalizer string is never actually added/removed and Kubernetes cannot proceed with deletion.

### The DeletionTimestamp is still useful

Even with the structured approach, your reconciler can still check `widget.DeletionTimestamp.IsZero()` to skip normal reconciliation work while the object is terminating. This case shows the pattern at `main.go:98`:

```go
if widget.DeletionTimestamp.IsZero() {
    logger.Info("reconciling live Widget", "color", widget.Spec.Color)
}
```

## How to run

```bash
cd 11-finalizers

# Install the CRD
kubectl apply -f widget_crd.yaml

# Start the controller
go run ./...
```

In a separate terminal, exercise the finalizer lifecycle:

```bash
# Create a Widget
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: blue-widget
  namespace: default
spec:
  color: blue
EOF

# Observe the controller adds the finalizer
kubectl get widget blue-widget -o jsonpath='{.metadata.finalizers}'
# ["example.com/widget-cleanup"]

# Delete the Widget — it enters terminating state
kubectl delete widget blue-widget

# Controller runs cleanup then removes the finalizer; object disappears
```

## What to observe

**On creation**, the controller logs:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="finalizer state updated"
time=2024-01-15T10:23:01.240+01:00 level=INFO msg="reconciling live Widget" color=blue
```

The first line indicates the finalizer string was added and `r.Update` was called. The second line is the normal reconcile path.

**On deletion**, the controller logs:

```
time=2024-01-15T10:24:30.100+01:00 level=INFO msg="running cleanup for Widget" name=blue-widget color=blue
time=2024-01-15T10:24:30.112+01:00 level=INFO msg="finalizer state updated"
```

The first line comes from `widgetCleanup.Finalize`. The second confirms the finalizer string was removed and the object can now be fully deleted.

Verify the object is gone after cleanup completes:

```bash
kubectl get widget blue-widget
# Error from server (NotFound): widgets.example.com "blue-widget" not found
```

If you stop the controller while a Widget is deleting, the object will stay in terminating state indefinitely — the finalizer prevents garbage collection until the controller returns to finish cleanup. Restart the controller to complete the deletion.

## Key code walkthrough

**Finalizer constant** (`main.go:31`):
```go
const widgetFinalizer = "example.com/widget-cleanup"
```
Reverse-DNS naming convention. This string appears verbatim in `metadata.finalizers` on the object.

**Cleanup implementation** (`main.go:59-70`):
```go
type widgetCleanup struct{}

func (f *widgetCleanup) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
    widget := obj.(*Widget)
    ctrl.LoggerFrom(ctx).Info("running cleanup for Widget", "name", widget.Name, "color", widget.Spec.Color)
    return finalizer.Result{}, nil
}
```
This is where real cleanup belongs — calling an external API, deleting cloud resources, etc. The type assertion to `*Widget` is safe because this handler is only registered for Widget objects.

**Registration at startup** (`main.go:123-124`):
```go
finalizers := finalizer.NewFinalizers()
if err := finalizers.Register(widgetFinalizer, &widgetCleanup{}); err != nil { ... }
```
Registration must happen before `mgr.Start()`. Multiple handlers can be registered under different names.

**The dispatch call** (`main.go:86-96`):
```go
result, err := r.finalizers.Finalize(ctx, widget)
if err != nil {
    return reconcile.Result{}, err
}
if result.Updated {
    if err := r.Update(ctx, widget); err != nil {
        return reconcile.Result{}, err
    }
}
```
This is the entire finalizer lifecycle in one block. No `DeletionTimestamp` check, no string manipulation — the library handles it.

## References

- [controller-runtime finalizer godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/finalizer)
- [upstream finalizer/finalizer.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/finalizer/finalizer.go)
- [Kubernetes finalizers documentation](https://kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/#foreground-deletion)
- [controller-runtime controllerutil godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil)
