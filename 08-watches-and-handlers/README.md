# Case 08: Watches and Handlers

The `ctrl.Builder` used in cases 02-07 is a convenience layer. Under it lies a lower-level API of `controller.New`, `source.Kind`, and `handler.*` — understanding this layer unlocks full control over what triggers your reconciler and how.

## What this case teaches

- `controller.New("name", mgr, controller.Options{Reconciler: r})` — create a controller without the builder
- `source.Kind(cache, &T{}, handler)` — a typed, generic event source that watches one resource type
- `handler.TypedEnqueueRequestForObject[*T]{}` — reconcile the changed object itself
- `handler.TypedEnqueueRequestForOwner[*Child](scheme, mapper, &Owner{}, ...)` — reconcile the owner when a child changes
- `handler.TypedEnqueueRequestsFromMapFunc[*T](fn)` — fan-out: one event triggers reconciles for multiple objects
- How these three Watch calls together replicate (and extend) what `For() + Owns()` does in the builder

## Key concepts

### Why go below the builder?

The builder's `.For()`, `.Owns()`, and `.Watches()` cover the common cases. But some controllers need to:
- Watch a resource that is not directly owned (e.g., a Namespace affecting all Widgets in it)
- Map one event to many reconcile requests (fan-out)
- Map many events to one reconcile request (fan-in)
- Apply different handlers to the same source type in different situations

For these patterns you drop down to `controller.New` + `c.Watch` and compose `source` and `handler` directly.

### What is `source.Kind`?

`source.Kind[T](cache, prototype, handler, predicates...)` is the generic typed event source for Kubernetes objects. It:
1. Registers with the Manager's informer cache to get events for the given type.
2. On each event, calls the `handler` to convert the event into zero or more `reconcile.Request` values.
3. Enqueues those requests into the controller's work queue.

The type parameter `T` and the `prototype` argument (`&Widget{}`) tell the cache which type to watch. The cache starts a List+Watch for that type if it hasn't already.

### What does `handler.TypedEnqueueRequestForObject` do?

The simplest possible handler: enqueue a request for the object that changed. This is exactly what the builder's `.For()` does internally. The `[*Widget]` type parameter constrains it to Widget events.

### What does `handler.TypedEnqueueRequestForOwner` do?

When a child object (ConfigMap) changes, this handler reads the child's `OwnerReferences`, finds the entry with `controller: true`, and enqueues a request for that owner (Widget). The `handler.OnlyControllerOwner()` option limits it to the single controlling owner, ignoring non-controller owner references. This is exactly what the builder's `.Owns()` does internally.

It needs the scheme and REST mapper to resolve the owner's GVK from the reference.

### What does `handler.TypedEnqueueRequestsFromMapFunc` do?

The most powerful handler. It accepts a function `func(ctx, *T) []reconcile.Request` that can return zero, one, or many requests in response to a single event. In this case, a Namespace event triggers a `List` of all Widgets in that namespace and returns a request for each one. This is a fan-out: one Namespace event → N Widget reconciles.

Real-world uses:
- A cluster-scoped Secret rotates → reconcile all Deployments that reference it
- A Namespace is labeled → reconcile all custom resources in that namespace
- A ConfigMap changes → reconcile all controllers that mount it

### How do these three Watch calls relate to the builder?

| Builder method | Equivalent low-level call |
|---|---|
| `.For(&Widget{})` | `c.Watch(source.Kind(cache, &Widget{}, TypedEnqueueRequestForObject[*Widget]{}))`|
| `.Owns(&corev1.ConfigMap{})` | `c.Watch(source.Kind(cache, &ConfigMap{}, TypedEnqueueRequestForOwner[*ConfigMap](...)))` |
| No direct equivalent | `c.Watch(source.Kind(cache, &Namespace{}, TypedEnqueueRequestsFromMapFunc[*Namespace](...)))` |

The builder is sugar over these calls. When the builder does not offer what you need, use the low-level API.

## How to run

Apply the CRD first:

```bash
cd 08-watches-and-handlers
kubectl apply -f widget_crd.yaml
```

Run the controller:

```bash
go run ./...
```

In another terminal, exercise all three watch paths:

```bash
# Watch 1: Widget event — reconcile the Widget itself
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: watch-test
  namespace: default
spec:
  color: purple
EOF

# Watch 2: ConfigMap with owner reference — reconcile the owner Widget
# Create a ConfigMap that claims watch-test as its controller owner
# (normally your reconciler creates this; here we do it manually for demonstration)
WIDGET_UID=$(kubectl get widget watch-test -n default -o jsonpath='{.metadata.uid}')
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: watch-test-config
  namespace: default
  ownerReferences:
  - apiVersion: example.com/v1alpha1
    kind: Widget
    name: watch-test
    uid: $WIDGET_UID
    controller: true
    blockOwnerDeletion: true
data:
  color: purple
EOF

# Edit the ConfigMap — should trigger a reconcile of the owner Widget
kubectl annotate configmap watch-test-config -n default touched=yes

# Watch 3: Namespace event — fan-out to all Widgets in that namespace
# The "default" namespace always exists; we just need to annotate it
kubectl annotate namespace default test-trigger=now --overwrite

# Clean up
kubectl delete widget watch-test -n default
kubectl delete crd widgets.example.com
```

## What to observe

```
# Watch 1: Widget created
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="reconciling Widget" key=default/watch-test

# Watch 2: ConfigMap annotated → owner Widget reconciled
time=2024-01-15T10:23:04.512+01:00 level=INFO msg="reconciling Widget" key=default/watch-test

# Watch 3: Namespace annotated → all Widgets in default namespace reconciled
time=2024-01-15T10:23:07.880+01:00 level=INFO msg="reconciling Widget" key=default/watch-test
```

All three Watch paths produce the same log line because they all enqueue a request for `watch-test` in `default`. The difference is which resource event triggered each reconcile. In a real controller you would add context to the log or use different reconcile paths to distinguish them.

## Key code walkthrough

**controller.New — low-level controller** (`main.go:88-92`):
```go
c, err := controller.New("widget-controller", mgr, controller.Options{Reconciler: r})
```
Returns a `controller.Controller` interface with a single `Watch` method. The builder wraps this and calls `Watch` on your behalf.

**Watch 1 — TypedEnqueueRequestForObject** (`main.go:95-99`):
```go
c.Watch(source.Kind(mgr.GetCache(), &Widget{},
    &handler.TypedEnqueueRequestForObject[*Widget]{}))
```
Enqueue a request for the Widget that changed. Direct replacement for `.For(&Widget{})`.

**Watch 2 — TypedEnqueueRequestForOwner** (`main.go:102-109`):
```go
c.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{},
    handler.TypedEnqueueRequestForOwner[*corev1.ConfigMap](
        mgr.GetScheme(), mgr.GetRESTMapper(), &Widget{},
        handler.OnlyControllerOwner(),
    )))
```
`handler.OnlyControllerOwner()` ensures only the entry with `controller: true` is used. Direct replacement for `.Owns(&corev1.ConfigMap{})`.

**Watch 3 — TypedEnqueueRequestsFromMapFunc** (`main.go:112-130`):
```go
c.Watch(source.Kind(mgr.GetCache(), &corev1.Namespace{},
    handler.TypedEnqueueRequestsFromMapFunc[*corev1.Namespace](
        func(ctx context.Context, ns *corev1.Namespace) []reconcile.Request {
            widgetList := &WidgetList{}
            mgr.GetClient().List(ctx, widgetList, client.InNamespace(ns.Name))
            reqs := make([]reconcile.Request, len(widgetList.Items))
            for i, w := range widgetList.Items {
                reqs[i] = reconcile.Request{Name: w.Name, Namespace: w.Namespace}
            }
            return reqs
        },
    )))
```
The map function is called for every Namespace event and returns one request per Widget in that namespace. Note: this runs in the informer goroutine, so it should be fast. The `List` call reads from the local cache, not the API server.

## References

- [controller.Controller godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller#Controller)
- [source package godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/source)
- [handler package godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/handler)
- [upstream source: pkg/controller/controller.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/controller/controller.go)
- [upstream source: pkg/source/source.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/source/source.go)
- [upstream source: pkg/handler/enqueue.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/handler/enqueue.go)
- [upstream source: pkg/handler/mapper.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/handler/mapper.go)
- [controller-runtime book: Watching Resources](https://book.kubebuilder.io/reference/watching-resources)
