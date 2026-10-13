# Case 10: Status Subresource

Write only the status of a custom resource without touching its spec, using controller-runtime's split status client.

## What this case teaches

- `r.Status().Update(ctx, obj)` — write only the `/status` subresource endpoint
- Why status updates are separated from spec updates in the Kubernetes API
- The `subresources: status: {}` CRD declaration that enables this separation
- How to pattern: fetch → do work → mutate status fields → `Status().Update()`
- The difference between `r.Update()` (full resource) and `r.Status().Update()` (status only)
- How `original.Status.Ready` versus `widget.Status.Ready` lets you log what changed

## Key concepts

### Why does the status subresource exist?

In Kubernetes, a resource has two conceptually distinct halves:

- **Spec** — the desired state, written by users and external controllers ("I want 3 replicas")
- **Status** — the observed state, written by the controller after it acts ("currently 2 replicas are ready")

If both lived at the same endpoint and the reconciler called `r.Update(ctx, widget)`, it would send the entire object back to the API server. This creates two problems:

1. **Spec drift**: The reconciler fetched the object, then someone else updated the spec before the reconciler's `Update` returned. The reconciler's `Update` would silently overwrite the new spec with the stale copy it fetched earlier.
2. **RBAC conflation**: You would need to grant the controller `update` on the full resource, which also grants it the ability to change the spec. The status subresource lets you grant `update` on `widgets/status` alone, which can only touch the status half.

Enabling `subresources: status: {}` in the CRD splits the resource into two endpoints: `widgets` (for spec) and `widgets/status` (for status). `r.Status().Update()` targets the second endpoint. The API server ignores any spec changes in the payload — it only applies the status fields.

### Why not use Status().Patch() instead?

`Status().Update()` replaces the entire status struct. `Status().Patch()` applies only the fields you include in the patch. For simple cases with a small status struct (like this Widget), `Update` is fine. For large or frequently-changing status objects, `Patch` reduces the risk of optimistic concurrency conflicts because it touches fewer fields. The pattern is the same either way — only the final write call changes.

### How the CRD enables it

The relevant stanza in `widget_crd.yaml`:

```yaml
subresources:
  status: {}
```

Without this, calling `r.Status().Update()` still works mechanically — it just writes to the same endpoint as `r.Update()` and the API server treats them identically. The separation only becomes meaningful once the CRD declares the subresource.

### The reconciler pattern

```
r.Get(ctx, key, widget)          # fetch current state (spec + status from cache)
original := widget.DeepCopy()    # snapshot for change detection / logging
widget.Status.Ready = true        # mutate only the status fields
r.Status().Update(ctx, widget)   # write only the status subresource
```

The `widget.Spec` fields are sent in the payload but the API server ignores them when writing to `/status`. Spec remains unchanged.

## How to run

```bash
cd 10-status-subresource

# Install the CRD (requires a running cluster)
kubectl apply -f widget_crd.yaml

# Start the controller
go run ./...
```

In a separate terminal, create a Widget to trigger reconciliation:

```bash
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: my-widget
  namespace: default
spec:
  color: blue
EOF
```

## What to observe

After creating the Widget, the controller logs:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="status updated" was=false now=true message="color \"blue\" is valid"
```

Confirm the status was written to the cluster:

```bash
kubectl get widget my-widget -o jsonpath='{.status}'
# {"message":"color \"blue\" is valid","ready":true}
```

Notice that `kubectl get widget my-widget -o yaml` shows both the spec you wrote and the status the controller wrote — they are stored together but written through separate API paths.

Try editing the spec while the controller is running:

```bash
kubectl patch widget my-widget --type=merge -p '{"spec":{"color":"red"}}'
```

The controller reconciles again and updates the message: `"color \"red\" is valid"`. The spec was never touched by the controller — only status.

## Key code walkthrough

**Status type definition** (`main.go:51-54`):
```go
type WidgetStatus struct {
    Ready   bool   `json:"ready,omitempty"`
    Message string `json:"message,omitempty"`
}
```
Both fields use `omitempty` so a fresh Widget with no status set does not emit empty values.

**Snapshot before mutation** (`main.go:76`):
```go
original := widget.DeepCopyObject().(*Widget)
```
Capturing the original state before mutation lets the reconciler log what changed (`was: false, now: true`). This is a common observability pattern.

**The key line** (`main.go:82`):
```go
if err := r.Status().Update(ctx, widget); err != nil {
    return reconcile.Result{}, err
}
```
`r.Status()` returns a `StatusClient` that routes to the `/status` endpoint. Swapping this to `r.Update(ctx, widget)` would overwrite the whole resource including spec.

**CRD subresource declaration** (`widget_crd.yaml:35-36`):
```yaml
subresources:
  status: {}
```
This one stanza is what makes the separation real on the API server side.

## References

- [controller-runtime client godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client)
- [StatusClient interface](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#StatusClient)
- [upstream client/interfaces.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/interfaces.go)
- [Kubernetes API Conventions — status subresource](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#spec-and-status)
