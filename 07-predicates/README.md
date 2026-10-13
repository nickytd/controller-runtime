# Case 07: Predicates

Predicates are filter functions that decide whether an event should trigger a reconcile. They are one of the most effective tools for reducing unnecessary reconcile calls and protecting against reconcile storms.

## What this case teaches

- `predicate.GenerationChangedPredicate` — skip status-only updates by comparing the `generation` field
- `predicate.NewPredicateFuncs` — build a custom predicate from a single `func(client.Object) bool`
- `predicate.Funcs` — specify per-event-type logic (CreateFunc, UpdateFunc, DeleteFunc, GenericFunc)
- `predicate.And` / `predicate.Or` — compose predicates with boolean logic
- `.WithEventFilter(predicate)` — apply a predicate to the primary resource watch on the builder
- Why predicates run in the event handler (before enqueuing), not in the reconciler

## Key concepts

### What is the generation field and why does it matter?

Every Kubernetes resource has a `metadata.generation` counter that the API server increments each time the `spec` is modified. Critically, the `generation` field does NOT increment when only the `status` is written. This asymmetry is intentional: the spec is the desired state (written by users), and the status is the observed state (written by controllers).

Without `GenerationChangedPredicate`, every `r.Status().Update(ctx, obj)` call your controller makes would trigger another reconcile, which would update the status again, which would trigger another reconcile — an infinite loop. `GenerationChangedPredicate` breaks this cycle by filtering out update events where the generation did not change, which includes all status-only writes.

Note: `generation` is only incremented for resources with a status subresource. For resources without one, the generation is always 0 and `GenerationChangedPredicate` would always return false for updates — avoid using it for such resources.

### Why run predicates in the event handler rather than the reconciler?

A predicate runs synchronously in the informer's event handler goroutine, before the work queue is touched. If the predicate returns `false`, the event is silently discarded with zero overhead. If you instead put the filter logic inside `Reconcile`, you still pay the cost of enqueuing the request, dequeuing it, and invoking the reconciler — even if you immediately return `Result{}, nil`. At high event rates (a busy cluster with thousands of ConfigMaps), this difference is significant.

### What is `predicate.NewPredicateFuncs`?

A shortcut that applies the same function to all four event types (Create, Update, Delete, Generic). It is equivalent to:
```go
predicate.Funcs{
    CreateFunc:  func(e CreateEvent) bool { return f(e.Object) },
    UpdateFunc:  func(e UpdateEvent) bool { return f(e.ObjectNew) },
    DeleteFunc:  func(e DeleteEvent) bool { return f(e.Object) },
    GenericFunc: func(e GenericEvent) bool { return f(e.Object) },
}
```
Use it when the filter logic is the same regardless of event type.

### What is `predicate.Funcs` for?

When you need different logic per event type, use `predicate.Funcs` and set only the fields you need. Unset fields default to `true` (pass all events of that type). The `logDeletes` predicate in this case only sets `DeleteFunc` — creates and updates pass through unchanged, while deletes are logged and also allowed through.

### How does `predicate.And` / `predicate.Or` compose?

`predicate.And(p1, p2)` returns true only if both predicates return true for a given event. `predicate.Or(p1, p2)` returns true if either returns true. They can be nested arbitrarily. The combined predicate in this case is:

```
(GenerationChanged AND in default namespace AND not a delete) OR (log delete and reconcile)
```

This means: reconcile if the spec changed in the default namespace, OR reconcile for any delete anywhere (so deleted resources are cleaned up).

### Ordering caveat: side effects behind an `Or` short-circuit

`predicate.Or` evaluates its arms left to right and returns `true` on the **first** arm that passes — it does not evaluate the rest. This matters when an arm has a side effect (like `logDeletes` logging each delete).

The `logDeletes` arm only runs if every earlier arm returns `false`. If the first arm (`combined`) already returned `true` for a delete, the `Or` short-circuits and `logDeletes.DeleteFunc` is never called — so the delete would be reconciled but never logged.

That is exactly why `combined` includes a `DeleteFunc` that returns `false`: `GenerationChangedPredicate` only overrides `Update` (its `Delete` inherits `predicate.Funcs` and defaults to `true`), and `onlyDefault` passes deletes in the default namespace. Without the explicit `false`, `combined.Delete` would short-circuit the `Or` for every default-namespace delete and the log line would be unreachable. Rejecting deletes on the `combined` arm forces every delete to fall through to the delete-aware arm.

**Takeaway:** if you want a predicate side effect to run on *every* event of some type, don't place it behind an `Or` arm that another predicate can satisfy first.

## How to run

```bash
cd 07-predicates
go run ./...
```

In another terminal, experiment with what does and does not trigger a reconcile:

```bash
# Create a ConfigMap in default — TRIGGERS reconcile (passes GenerationChanged on create)
kubectl create configmap pred-test -n default --from-literal=key=val

# Annotate it (spec change, generation increments) — TRIGGERS reconcile
kubectl annotate configmap pred-test -n default note=updated

# Create a ConfigMap in kube-system — DOES NOT trigger reconcile (wrong namespace)
kubectl create configmap pred-test-sys -n kube-system --from-literal=key=val

# Delete the kube-system one — TRIGGERS reconcile (delete predicate returns true everywhere)
kubectl delete configmap pred-test-sys -n kube-system

# Status-only update — DOES NOT trigger reconcile (generation unchanged)
# (ConfigMaps don't have a status subresource, so this is hard to demonstrate directly;
# the GenerationChanged predicate is most visible on custom resources with status subresources)

# Delete the default one — TRIGGERS reconcile AND logs the delete
kubectl delete configmap pred-test -n default
```

## What to observe

In the controller logs:

```
# Creating pred-test in default — reconcile fires
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="reconcile triggered (passed all predicates)" key=default/pred-test

# Creating pred-test-sys in kube-system — NO reconcile log (filtered by namespace predicate)

# Deleting pred-test-sys in kube-system — delete predicate fires AND logs separately
time=2024-01-15T10:23:08.900+01:00 level=INFO msg="delete event seen" name=pred-test-sys
time=2024-01-15T10:23:08.901+01:00 level=INFO msg="reconcile triggered (passed all predicates)" key=kube-system/pred-test-sys

# Deleting pred-test in default — also logs AND reconciles
time=2024-01-15T10:23:12.100+01:00 level=INFO msg="delete event seen" name=pred-test
time=2024-01-15T10:23:12.101+01:00 level=INFO msg="reconcile triggered (passed all predicates)" key=default/pred-test
```

The delete log fires for deletes in *every* namespace — including `default`. This works only because the `combined` arm rejects deletes, so every delete falls through to `logDeletes`. If `combined` had passed default-namespace deletes (its natural default), the `Or` would short-circuit on that arm and the log for a `default` delete would never appear even though the reconcile still ran. See the "Ordering caveat" section above.

## Key code walkthrough

**GenerationChangedPredicate + custom namespace filter** (`main.go:52-69`):
```go
onlyDefault := predicate.NewPredicateFuncs(func(o client.Object) bool {
    return o.GetNamespace() == "default"
})
combined := predicate.And(
    predicate.GenerationChangedPredicate{},
    onlyDefault,
    predicate.Funcs{
        DeleteFunc: func(event.DeleteEvent) bool { return false },
    },
)
```
All three conditions must pass for the event to be enqueued via this arm. The `DeleteFunc` returning `false` deliberately excludes deletes from this arm so they are handled (and logged) by `logDeletes` — see the "Ordering caveat" section.

**Delete-aware predicate with side effects** (`main.go:78-83`):
```go
logDeletes := predicate.Funcs{
    DeleteFunc: func(e event.DeleteEvent) bool {
        ctrl.Log.Info("delete event seen", "name", e.Object.GetName())
        return true
    },
}
```
Only `DeleteFunc` is set; Create/Update/Generic default to `true`. Side effects (logging) inside a predicate are allowed but should be lightweight — the predicate runs synchronously in the informer goroutine.

**Applying the composed predicate** (`main.go:85-88`):
```go
ctrl.NewControllerManagedBy(mgr).
    For(&corev1.ConfigMap{}).
    WithEventFilter(predicate.Or(combined, logDeletes)).
    Complete(...)
```
`.WithEventFilter` applies to the primary `For()` resource. To apply different predicates to `.Owns()` or `.Watches()` resources, pass them as options to those calls.

## References

- [predicate package godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/predicate)
- [upstream source: pkg/predicate/predicate.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/predicate/predicate.go)
- [upstream source: pkg/builder/controller.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/builder/controller.go)
- [controller-runtime book: Predicates](https://book.kubebuilder.io/reference/watching-resources/operator-managed)
- [Kubernetes metadata.generation semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions)
