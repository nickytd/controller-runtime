# Case 06: Owns and Owned Resources

Owner references and the `.Owns()` watch are the foundation of the "desired-state" pattern where a parent resource manages the lifecycle of child resources it creates.

## What this case teaches

- How to set an owner reference on a child object with `ctrl.SetControllerReference(owner, child, scheme)`
- How `.Owns(&corev1.ConfigMap{})` on the builder causes child changes to re-queue the parent Widget
- How `controllerutil.CreateOrUpdate(ctx, client, obj, mutateFn)` handles the create-or-update idempotently
- Why owner references enable cascading deletion (garbage collection)
- The reconcile cascade: Widget changes → reconcile Widget → create/update child ConfigMap → child change event → re-queue Widget again

## Key concepts

### What is an owner reference?

An owner reference is a metadata field on a child object that points to its parent: it carries the parent's GVK, name, and UID. The Kubernetes garbage collector watches for objects whose owner has been deleted and removes them automatically. This is how `kubectl delete widget my-widget` also removes `my-widget-config` without your controller writing any deletion logic.

The `uid` field in the owner reference is critical: if the parent is deleted and recreated with the same name, the new parent gets a different UID, so the old orphaned child is still collected correctly.

### Why use `ctrl.SetControllerReference` instead of setting `OwnerReferences` directly?

`ctrl.SetControllerReference` enforces the constraint that only one owner can have `controller: true` in the owner references list. This matters because the `.Owns()` watch uses that flag to find which parent to re-queue when a child changes. If you set the raw `OwnerReferences` field yourself, you might accidentally create multiple controller owners, which confuses the garbage collector and the watch.

The function also requires the scheme to look up the parent's GVK — it needs to know `"Widget"` maps to `example.com/v1alpha1/Widget` to write the correct `apiVersion` and `kind` into the reference.

### What does `controllerutil.CreateOrUpdate` do?

It is a read-modify-write helper that:
1. Tries to `Get` the object by its `ObjectMeta`.
2. If not found: calls the `mutateFn` to populate fields, then calls `Create`.
3. If found: calls the `mutateFn` with the existing object (so you can update fields), then calls `Update`.

The `mutateFn` is your desired-state function. It should set `Data`, `Spec`, or any fields your controller owns. You must not change `ObjectMeta.Name`, `ObjectMeta.Namespace`, or `ObjectMeta.ResourceVersion` inside the mutate function.

The return value (`controllerutil.OperationResult`) is one of `"unchanged"`, `"created"`, or `"updated"` — useful for logging.

### Why does the child change trigger a reconcile of the parent?

`.Owns(&corev1.ConfigMap{})` registers an event handler on the informer for ConfigMaps. That handler looks at the ConfigMap's `OwnerReferences`, finds the entry with `controller: true`, and enqueues a `Request` for that owner Widget. This means: if someone manually edits or deletes the child ConfigMap, the parent Widget is reconciled and will restore the desired state. This is the self-healing property of the control loop.

### Why does the reconciler need the `Scheme`?

`ctrl.SetControllerReference` needs the scheme to resolve the parent Widget's GVK. Without it, the owner reference would have an empty `apiVersion` and `kind`, which the garbage collector would not understand.

## How to run

Apply the CRD first (same CRD as case 05):

```bash
cd 06-owns-and-owned
kubectl apply -f widget_crd.yaml
```

Run the controller:

```bash
go run ./...
```

In another terminal, create a Widget and observe the child ConfigMap:

```bash
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: red-widget
  namespace: default
spec:
  color: red
EOF

# The controller creates red-widget-config automatically
kubectl get configmap red-widget-config -n default
kubectl get configmap red-widget-config -n default -o jsonpath='{.data}'
# {"color":"red"}

# See the owner reference
kubectl get configmap red-widget-config -n default -o jsonpath='{.metadata.ownerReferences}'

# Self-healing: manually delete the child — it should be recreated
kubectl delete configmap red-widget-config -n default
sleep 2
kubectl get configmap red-widget-config -n default  # recreated

# Change the Widget color — the child ConfigMap should update
kubectl patch widget red-widget -n default --type=merge -p '{"spec":{"color":"green"}}'
kubectl get configmap red-widget-config -n default -o jsonpath='{.data}'
# {"color":"green"}

# Cascading delete — deleting the Widget removes the ConfigMap too
kubectl delete widget red-widget -n default
kubectl get configmap red-widget-config -n default  # should be gone
```

## What to observe

In the controller logs:

```
# First reconcile — child does not exist yet
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="child ConfigMap" result=created

# After manual deletion of child — self-healing
time=2024-01-15T10:23:05.101+01:00 level=INFO msg="child ConfigMap" result=created

# After patching Widget color — child updated
time=2024-01-15T10:23:09.512+01:00 level=INFO msg="child ConfigMap" result=updated

# After no change — idempotent, child already matches desired state
time=2024-01-15T10:23:12.880+01:00 level=INFO msg="child ConfigMap" result=unchanged
```

The `result` field from `controllerutil.CreateOrUpdate` shows whether the API server was actually mutated. When the desired state already matches, the call results in `result=unchanged` — no unnecessary API calls.

## Key code walkthrough

**SetControllerReference** (`main.go:93-95`):
```go
if err := ctrl.SetControllerReference(widget, cm, r.Scheme); err != nil {
    return reconcile.Result{}, err
}
```
Must be called before `CreateOrUpdate`. The owner reference is set on `cm.ObjectMeta.OwnerReferences` in memory; `CreateOrUpdate` then writes it to the API server.

**CreateOrUpdate with mutateFn** (`main.go:98-101`):
```go
result, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
    cm.Data = map[string]string{"color": widget.Spec.Color}
    return nil
})
```
The closure captures `widget` from the outer scope. Every time it is called (on both create and update paths), it sets `Data` to the current desired state derived from the Widget spec.

**Wiring `.Owns()`** (`main.go:133-136`):
```go
ctrl.NewControllerManagedBy(mgr).
    For(&Widget{}).
    Owns(&corev1.ConfigMap{}).
    Complete(&WidgetReconciler{...})
```
`.Owns()` registers a secondary watch on ConfigMaps. Events on ConfigMaps flow through the owner-reference handler, which re-queues the controlling Widget rather than the ConfigMap itself.

## References

- [controllerutil.CreateOrUpdate godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil#CreateOrUpdate)
- [ctrl.SetControllerReference godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil#SetControllerReference)
- [upstream source: pkg/controller/controllerutil/controllerutil.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/controller/controllerutil/controllerutil.go)
- [upstream source: pkg/builder/controller.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/builder/controller.go)
- [Kubernetes garbage collection and owner references](https://kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/)
