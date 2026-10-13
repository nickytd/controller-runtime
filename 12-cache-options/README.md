# Case 12: Cache Options

Scope the controller's informer cache to a specific namespace and label selector, and strip `managedFields` from all cached objects to reduce memory usage.

## What this case teaches

- `cache.Options.ByObject` — restrict which objects a particular type's informer caches
- `cache.ByObject{Namespaces: map[string]cache.Config{...}}` — per-namespace scoping with optional label selectors
- `cache.Options.DefaultTransform` — apply a transform function to every object as it enters the cache
- `cache.TransformStripManagedFields()` — built-in transform that removes `metadata.managedFields` to save RAM
- `cache.ErrCacheNotStarted` — the error returned when you read from the cache before `mgr.Start()` completes
- Why namespace-scoped operators use cache options rather than client-side filtering

## Key concepts

### What is the controller-runtime cache?

Every manager runs an in-process cache backed by Kubernetes informers. When your reconciler calls `r.Get()` or `r.List()`, it does not call the API server — it reads from this local cache. The cache starts a watch on the API server and keeps itself up to date. This dramatically reduces API server load in clusters with many controllers.

By default, the cache watches all namespaces for all registered types. For a cluster-admin operator this is fine. But many operators are designed to be scoped: they manage resources in exactly one namespace, or only resources with a specific label. Caching everything else wastes memory and exposes data the operator should not see.

### cache.Options.ByObject

`ByObject` is a map from object type to `cache.ByObject`. Each entry restricts the informer for that type:

```go
cache.Options{
    ByObject: map[client.Object]cache.ByObject{
        &corev1.ConfigMap{}: {
            Namespaces: map[string]cache.Config{
                "default": {
                    LabelSelector: labels.SelectorFromSet(labels.Set{"app": "cache-demo"}),
                },
            },
        },
    },
}
```

This tells the cache: "only watch ConfigMaps in the `default` namespace that have the label `app=cache-demo`." The controller will never see a ConfigMap from `kube-system`, and it will never see a ConfigMap in `default` that lacks the label. From the reconciler's perspective, those objects do not exist — `r.Get()` and `r.List()` will return nothing for them.

The `Namespaces` map can contain multiple keys to watch multiple namespaces with different label selectors per namespace. Using the `cache.Config{}` zero value (no label selector) watches all objects of that type in the given namespace.

### DefaultTransform and stripping managedFields

`metadata.managedFields` is the server-side apply field ownership record. It can be kilobytes per object. In a cluster with thousands of ConfigMaps, this adds up. Controllers rarely need to read `managedFields` — they just want to know the object's spec and status.

`cache.TransformStripManagedFields()` is a built-in transform that removes this field from every object as it is stored in the cache. The in-memory representation is smaller, but the actual object in etcd is unchanged. The reconciler in this case confirms the transform works:

```go
logger.Info("reconciled", "name", cm.Name, "managedFields", len(cm.ManagedFields))
// managedFields: 0
```

You can write your own `DefaultTransform` function for any other trimming — removing annotations you do not care about, zeroing out large fields, etc. The function signature is:

```go
func(obj interface{}) (interface{}, error)
```

### cache.ErrCacheNotStarted

The cache-backed client can only serve reads after the informer has done its initial list+watch from the API server. If you call `r.List()` or `r.Get()` before `mgr.Start()` completes the cache warm-up, you get `cache.ErrCacheNotStarted`. This is a common pitfall when writing setup code outside the reconciler — for example, in a `main()` function that tries to read cluster state before calling `mgr.Start()`. The fix is to move such reads into a `Runnable` (registered via `mgr.Add()`) which only runs after the cache is ready, or to use an uncached client (`mgr.GetAPIReader()`).

## How to run

```bash
cd 12-cache-options
go run ./...
```

No CRD installation needed — this case watches ConfigMaps (a built-in type).

In a separate terminal, create ConfigMaps to observe the scoping:

```bash
# This ConfigMap WILL be reconciled (in-scope: correct namespace + label)
kubectl create configmap in-scope \
  --namespace default \
  --from-literal=key=value \
  -o yaml --dry-run=client | \
  kubectl label -f - app=cache-demo --local -o yaml | \
  kubectl apply -f -

# Equivalent one-liner:
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: in-scope
  namespace: default
  labels:
    app: cache-demo
data:
  key: value
EOF

# This ConfigMap will NOT be reconciled (wrong label)
kubectl create configmap out-of-scope --namespace default --from-literal=key=value

# This ConfigMap will NOT be reconciled (wrong namespace)
kubectl -n kube-system create configmap out-of-ns --from-literal=key=value
```

## What to observe

Only the `in-scope` ConfigMap triggers the reconciler:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg=reconciled name=in-scope managedFields=0
```

The `out-of-scope` and `out-of-ns` ConfigMaps produce no log output — the cache never received them, so no reconcile event was ever generated.

The `managedFields: 0` confirms `TransformStripManagedFields()` is working. Without the transform, this value would be 1 or more (one entry per field manager that has touched the object).

## Key code walkthrough

**Cache scoping** (`main.go:58-66`):
```go
ByObject: map[client.Object]cache.ByObject{
    &corev1.ConfigMap{}: {
        Namespaces: map[string]cache.Config{
            "default": {
                LabelSelector: labels.SelectorFromSet(labels.Set{"app": "cache-demo"}),
            },
        },
    },
},
```
The map key is an empty typed object used only for its type identity. The value is the scope. Multiple namespace entries are supported.

**Strip transform** (`main.go:68`):
```go
DefaultTransform: cache.TransformStripManagedFields(),
```
`DefaultTransform` applies to every object type in the cache. To apply different transforms per type, use `cache.ByObject.Transform` instead.

**Reconciler observing the result** (`main.go:38`):
```go
logger.Info("reconciled", "name", cm.Name, "managedFields", len(cm.ManagedFields))
```
The length will always be 0 because the transform ran before the object was stored in the cache.

## References

- [controller-runtime cache godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/cache)
- [cache.Options godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/cache#Options)
- [upstream cache/cache.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/cache/cache.go)
- [upstream cache/multi_namespace_cache.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/cache/multi_namespace_cache.go)
