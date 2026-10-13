# Case 03: Client CRUD

The controller-runtime client is a unified interface for reading from the local cache and writing to the Kubernetes API server — understanding the read/write split is critical for writing correct controllers.

## What this case teaches

- How to obtain the cache-backed client via `mgr.GetClient()` and embed it in a reconciler
- How to read a single object with `r.Get(ctx, namespacedName, obj)`
- How to list objects with `r.List(ctx, list, ...ListOptions)` using `client.InNamespace` and `client.MatchingLabels`
- How to create objects with `r.Create(ctx, obj)` and handle "already exists" gracefully
- What `client.IgnoreNotFound(err)` and `client.IgnoreAlreadyExists(err)` do and why they exist
- Why reads go through the cache and writes go directly to the API server

## Key concepts

### Why is the client cache-backed for reads?

Every Kubernetes controller runs in a tight reconcile loop that can fire many times per minute. If every `r.Get` made a network round-trip to the API server, you would quickly exhaust the API server's request quota and slow down the entire cluster. controller-runtime solves this with an informer cache: on startup the Manager performs a List+Watch for every type it knows about, keeps an in-memory copy up-to-date, and serves all `Get` and `List` calls from that copy. The cache is always consistent with `etcd` within the informer's watch latency (typically well under a second).

### Why do writes bypass the cache?

The cache is read-only by design. Writing to the cache would create a local shadow that diverges from the actual cluster state. Instead, `Create`, `Update`, `Patch`, and `Delete` go directly to the API server, which then triggers watch events that update the cache. This round-trip is intentional — it ensures the cache always reflects what the API server accepted, not what your controller hoped to write.

### What does `client.IgnoreNotFound(err)` do?

When you call `r.Get` and the object has been deleted, the API returns a 404. This is represented as an `apierrors.IsNotFound` error. `client.IgnoreNotFound(err)` returns `nil` for that specific error and passes all other errors through. The pattern `return reconcile.Result{}, client.IgnoreNotFound(err)` is idiomatic for handling deletion: if the object is gone, there is nothing to reconcile, so return success.

### What does `client.IgnoreAlreadyExists(err)` do?

The mirror of `IgnoreNotFound`. When you call `r.Create` and the object already exists (because a previous reconcile already created it), you get a 409 Conflict. Since "the object already exists" is often the desired outcome of a Create call in a controller, this helper lets you write `client.IgnoreAlreadyExists(err) != nil` to skip the already-exists case gracefully. For production code, prefer `controllerutil.CreateOrUpdate` (case 06) which handles this more completely.

### Why embed `client.Client` in the reconciler struct?

Embedding `client.Client` promotes all client methods (`Get`, `List`, `Create`, etc.) directly onto the reconciler, so you can call `r.Get(...)` instead of `r.Client.Get(...)`. It is purely a Go convenience pattern. Alternatively you can name the field: `type R struct { client client.Client }` — either works.

## How to run

```bash
cd 03-client-crud
go run ./...
```

In another terminal:

```bash
# Trigger reconcile — the reconciler will create a child ConfigMap named "crud-child"
kubectl create configmap trigger-cm --from-literal=hello=world -n default

# Verify the child was created
kubectl get configmap crud-child -n default

# See the label the child was created with
kubectl get configmap crud-child -n default -o jsonpath='{.metadata.labels}'
# Output: {"app":"crud-demo"}

# List everything the label selector would match
kubectl get configmap -n default -l app=crud-demo

# Clean up
kubectl delete configmap trigger-cm crud-child -n default
```

## What to observe

In the controller logs (the manager uses `slog.NewTextHandler`, so output is `key=value` text, not JSON):

```
time=2026-10-09T13:28:21.283+03:00 level=INFO msg="got ConfigMap" "data keys"=1
time=2026-10-09T13:28:21.284+03:00 level=INFO msg="listed ConfigMaps with label" count=0
```

On the first reconcile, `count` is 0 because `crud-child` does not yet exist. After the first reconcile creates it, any subsequent reconcile of `trigger-cm` will see `count=1`. Notice that creating `crud-child` itself triggers another reconcile (it is a ConfigMap, and the controller watches all ConfigMaps). That second reconcile attempts to create `crud-child` again and gets an "already exists" error, which `IgnoreAlreadyExists` silently discards.

This demonstrates the idempotency requirement: your controller must handle being called multiple times for the same state.

## Key code walkthrough

**Embedding the client** (`main.go:25-27`):
```go
type CRUDReconciler struct {
    client.Client
}
```
The client is passed in at construction time (`mgr.GetClient()`) and all methods are promoted.

**Get with IgnoreNotFound** (`main.go:33-37`):
```go
if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
    return reconcile.Result{}, client.IgnoreNotFound(err)
}
```
If the ConfigMap was deleted between the event being enqueued and this code running, we return success. Any other error (network failure, RBAC denial) is propagated so the controller retries.

**List with options** (`main.go:40-47`):
```go
r.List(ctx, cmList,
    client.InNamespace(req.Namespace),
    client.MatchingLabels{"app": "crud-demo"},
)
```
`ListOptions` are variadic and composable. `InNamespace` restricts to one namespace; `MatchingLabels` applies a label selector. Because this List is served from the informer cache, the selector is evaluated in-memory against the cached objects (no network call to the API server) — not pushed down as a server-side `labelSelector` query.

**Create with IgnoreAlreadyExists** (`main.go:50-58`):
The desired object is constructed with its `ObjectMeta` fields (`Name`, `Namespace`, `Labels`) and `Data`, then created. `client.IgnoreAlreadyExists(err) != nil` returns true only when there is a real error (not 409), triggering a requeue.

## References

- [client.Client godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#Client)
- [upstream source: pkg/client/interfaces.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/interfaces.go)
- [upstream source: pkg/client/options.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/options.go)
- [upstream source: pkg/client/object.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/object.go)
- [controller-runtime book: Using the client](https://book.kubebuilder.io/cronjob-tutorial/controller-implementation)
