# Case 15: Server-Side Apply

Declare desired state and let the API server merge it, with fine-grained field ownership — so multiple actors can each manage their own fields of the same object without conflict.

## What this case teaches

- `r.Apply(ctx, desired, client.FieldOwner("name"), client.ForceOwnership)` — the typed SSA call
- `corev1ac.ConfigMap(name, namespace).WithData(...)` — building an *apply configuration*, the strongly-typed input to `client.Apply`
- `client.FieldOwner("name")` — declares which controller "owns" the fields it sets
- `client.ForceOwnership` — take ownership of fields currently owned by another actor
- The idempotency guarantee: applying the same desired state twice is a server-side no-op — and why that is what makes a reconciler *settle* instead of hot-looping
- Why a cluster-wide controller must scope its watch (here: a label predicate) before it writes

## Key concepts

### What is server-side apply?

Traditional Kubernetes updates require the client to:
1. `GET` the current object
2. Modify the in-memory copy
3. `PUT` the entire object back

This three-way merge happens in the client, which means the client must know what the "original" state was to compute the diff correctly. This is fragile: if two controllers both try to manage parts of the same object, they overwrite each other's changes.

Server-side apply (SSA) moves the merge to the API server. The client sends only the fields it cares about ("the desired state"), and the API server merges them into the existing object using field ownership metadata. Each field has an owner; a controller can only modify fields it owns, and the API server rejects or warns about conflicts with fields owned by other actors.

### The apply-configuration API

Modern controller-runtime (`client.Client.Apply`) takes a **apply configuration** rather than a plain typed object. These are generated builders under `k8s.io/client-go/applyconfigurations/...` — for a ConfigMap, `corev1ac.ConfigMap(name, namespace)`:

```go
import corev1ac "k8s.io/client-go/applyconfigurations/core/v1"

desired := corev1ac.ConfigMap(req.Name, req.Namespace).
    WithData(map[string]string{
        "managed-by": fieldManager,
        "reconciled": "true",
    })

r.Apply(ctx, desired, client.FieldOwner(fieldManager), client.ForceOwnership)
```

The builder carries the GVK internally, so — unlike the older `r.Patch(obj, client.Apply)` form — you do **not** hand-set `TypeMeta` (`APIVersion`/`Kind`). An apply configuration only ever contains the fields you explicitly set via `WithX`, which makes "send only what I own" the natural default: anything you don't call `WithX` for is simply absent from the payload and therefore left to its existing owner.

> Earlier controller-runtime versions expressed SSA as `r.Patch(obj, client.Apply, ...)`. That `client.Apply` patch constant is now deprecated in favor of `client.Client.Apply` with an apply configuration; this sample uses the current API.

### Field ownership

Every field in every Kubernetes object has an owner tracked in `metadata.managedFields`. When you call `r.Apply` with `client.FieldOwner("ssa-lab-controller")`, the API server records that `ssa-lab-controller` owns `data.managed-by` and `data.reconciled`. If another controller or user tries to set those same fields, the API server returns a conflict error — unless `client.ForceOwnership` is set, in which case ownership is transferred.

This is the key difference from `r.Update()` or `client.MergeFrom`: SSA ownership is tracked on the server. You do not need to do a GET before an apply; you just send what you want.

### Idempotency is what makes a reconciler settle

Applying the same apply configuration twice is a **server-side no-op**: when the fields you own already hold the values you declare, the API server does not bump `resourceVersion`. No new version means no watch event, which means your controller is not re-enqueued — the reconcile loop quiesces after one pass.

This is not just a nicety; it is load-bearing. A reconciler that writes to an object on *every* pass — even a write that changes nothing semantically — bumps `resourceVersion` each time, re-triggers its own watch, and hot-loops forever. SSA's idempotent apply is precisely what lets a correctly-scoped controller reach a steady state.

### Scoping the watch

`For(&corev1.ConfigMap{})` watches **every** ConfigMap in the cluster — including `kube-root-ca.crt`, which exists in every namespace. A controller that writes to each object it observes must therefore scope itself, or it will rewrite system objects in a loop.

This sample scopes with a label predicate so it only touches ConfigMaps that opt in:

```go
onlyLabeled := predicate.NewPredicateFuncs(func(o client.Object) bool {
    _, ok := o.GetLabels()["ssa-lab"]
    return ok
})

ctrl.NewControllerManagedBy(mgr).
    For(&corev1.ConfigMap{}).
    WithEventFilter(onlyLabeled).
    Complete(&SSAReconciler{Client: mgr.GetClient()})
```

The predicate runs on watch events *before* they reach the work queue, so unlabeled ConfigMaps never cause a reconcile. (See case 07 for predicates in depth.)

### client.ForceOwnership

If another actor currently owns a field you are trying to set, the apply returns a 409 Conflict. `client.ForceOwnership` overrides this and steals the field ownership. Use it with care: it is appropriate for a controller that should always be authoritative for its fields, but overusing it defeats the purpose of SSA's conflict detection.

## How to run

```bash
cd 15-server-side-apply
go run ./...
```

No CRD installation needed — this case manages built-in ConfigMaps.

With no opted-in ConfigMaps present, the controller starts and stays quiet — it reconciles nothing, because the label predicate filters out every ConfigMap in the cluster. In a separate terminal, create a ConfigMap that opts in via the `ssa-lab` label:

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-ssa
  namespace: default
  labels:
    ssa-lab: "true"
data:
  pre-existing-key: my-value
EOF
```

## What to observe

After the labeled ConfigMap is created, the controller logs an apply — then **goes quiet**:

```
time=2026-10-12T09:21:54.001+02:00 level=INFO msg="SSA applied" name=test-ssa data="map[managed-by:ssa-lab-controller reconciled:true]"
```

You will see one or two applies as the object converges (the first apply adds the fields, bumping `resourceVersion` once), and then nothing. That silence is the point: the idempotent apply reached a fixed point. Remove the label scoping or re-introduce a per-reconcile write of changing data, and the controller would hot-loop instead.

Inspect the resulting ConfigMap:

```bash
kubectl get configmap test-ssa -o yaml
```

You will see:
- `data.pre-existing-key: my-value` — the field *you* set with `kubectl`, still present (SSA does not remove fields it does not own)
- `data.managed-by: ssa-lab-controller` — added by SSA
- `data.reconciled: "true"` — added by SSA
- `metadata.managedFields` — contains entries showing which actor owns which fields

To see the ownership explicitly:

```bash
kubectl get configmap test-ssa \
  -o jsonpath='{.metadata.managedFields}' | jq .
```

You will see an entry with `manager: ssa-lab-controller` listing `data.managed-by` and `data.reconciled` as owned fields, and a separate `kubectl` entry owning `data.pre-existing-key` and the `ssa-lab` label — proof that the two actors coexist without clobbering each other.

Touch the ConfigMap to force another reconcile:

```bash
kubectl annotate configmap test-ssa noop=1 --overwrite
```

The controller reconciles again and the apply is identical — the result does not change, demonstrating idempotency. No update is sent to etcd for the fields it owns because nothing changed.

## Key code walkthrough

**The apply configuration** (`main.go:46-50`):
```go
desired := corev1ac.ConfigMap(req.Name, req.Namespace).
    WithData(map[string]string{
        "managed-by": fieldManager,
        "reconciled": "true",
    })
```
The builder carries GVK internally — no `TypeMeta` to set. Only the fields you add via `WithX` are in the payload, so only those become owned.

**The SSA apply call** (`main.go:62-65`):
```go
r.Apply(ctx, desired,
    client.FieldOwner(fieldManager),
    client.ForceOwnership,
)
```
`FieldOwner` sets the `fieldManager` query parameter on the request. `ForceOwnership` sets `force=true`. Re-running this with identical `desired` is a server-side no-op.

**Scoping the watch** (`main.go:88-95`):
```go
onlyLabeled := predicate.NewPredicateFuncs(func(o client.Object) bool {
    _, ok := o.GetLabels()[labelKey]
    return ok
})
// ...
.WithEventFilter(onlyLabeled)
```
Without this, the controller would observe and rewrite every ConfigMap in the cluster, re-triggering its own watch endlessly.

**Field manager constant** (`main.go:29`):
```go
const fieldManager = "ssa-lab-controller"
```
Consistent naming across calls ensures managedFields attribution is stable and queryable.

## References

- [controller-runtime client godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client)
- [Client.Apply](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#Client)
- [k8s.io/client-go applyconfigurations](https://pkg.go.dev/k8s.io/client-go/applyconfigurations/core/v1)
- [upstream client/apply.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/apply.go)
- [Kubernetes server-side apply documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/)
- [Kubernetes managedFields documentation](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management)