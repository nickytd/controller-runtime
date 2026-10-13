# Case 13: Field Indexer

Register an in-process index over cached objects so that cross-resource lookups are fast constant-time operations instead of full scans.

## What this case teaches

- `mgr.GetFieldIndexer().IndexField(ctx, objType, indexKey, extractFn)` — register a field index before `Start()`
- `extractFn func(client.Object) []string` — the function that extracts the indexed value(s) from each object
- `client.MatchingFields{indexKey: value}` — use the index in a `List` call
- How this case combines two watches: Nodes (primary resource) and Pods (looked up via index)
- The performance trade-off: in-cache index vs full scan vs extra API calls
- Why the index key mirrors the JSON path of the field being indexed (`.spec.nodeName`)

## Key concepts

### The problem without field indexes

Imagine a controller that reconciles Nodes. On each reconcile it needs to know which Pods are running on that Node. Without an index, the only options are:

1. **Full scan**: `r.List(ctx, podList)` to get all Pods cluster-wide, then loop through `podList.Items` to find those where `pod.Spec.NodeName == req.Name`. This is O(total pods) for every reconcile of every node. In a large cluster this is thousands of objects scanned thousands of times per minute.

2. **Extra API server call**: Use `mgr.GetAPIReader()` to bypass the cache and call the API server with a field selector. This adds latency and load to the API server for every reconcile.

3. **Field index**: Register the index once at startup. The cache maintains a reverse map from field value to object key. `r.List(ctx, podList, client.MatchingFields{".spec.nodeName": "node-1"})` is an O(1) lookup in that map. The cache returns only the matching Pods. No API server call, no linear scan.

### How IndexField works

```go
mgr.GetFieldIndexer().IndexField(ctx,
    &corev1.Pod{},          // which type to index
    ".spec.nodeName",       // the index key (arbitrary string, convention is JSON path)
    func(obj client.Object) []string {
        pod := obj.(*corev1.Pod)
        if pod.Spec.NodeName == "" {
            return nil
        }
        return []string{pod.Spec.NodeName}
    },
)
```

The `extractFn` is called for every Pod as it enters the cache (on startup and on every update). It returns the value(s) to index — a slice because some fields can contain multiple values (e.g., a Pod's owner references). The cache stores the reverse mapping: `".spec.nodeName" → "node-1"` → `[pod-a, pod-b, pod-c]`.

The index key `.spec.nodeName` is a convention, not a literal JSON path evaluated by a library. You could name it `nodeName` or `byNode` — the convention of using the JSON path makes indexes self-documenting.

### Why must IndexField be called before Start()?

The field indexer needs to populate its map by processing all existing objects when the informer does its initial list. If you register an index after `mgr.Start()`, the cache has already been populated and the index would be incomplete. Controller-runtime panics or returns an error if you call `IndexField` after the cache has started.

The `context.Background()` passed to `IndexField` here is only for the synchronous registration call, not for the indexing goroutine itself.

### The reconcile pattern

This case reconciles Nodes, not Pods. When a Node changes, the reconciler wants to know about its workload:

```go
// Reconcile is called with req.Name = "node-1" (the node name)
podList := &corev1.PodList{}
r.List(ctx, podList,
    client.MatchingFields{podNodeNameIndex: req.Name},
)
// podList.Items contains only Pods scheduled on this node
```

No Pod watch is needed — the Pod cache is already maintained by the index. Changes to Pods do not trigger the Node reconciler here; only Node events do. A more complete operator would also `Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(...))` to re-reconcile the node when a Pod on it changes.

## How to run

```bash
cd 13-field-indexer
go run ./...
```

No CRD installation needed — this case indexes built-in Pod objects and watches built-in Node objects.

## What to observe

On startup, the controller reconciles all existing Nodes. For each node you will see:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="pods on node" node=kind-control-plane count=12
```

The `count` reflects how many Pods have `spec.nodeName` set to that node. The lookup is served entirely from the in-process cache.

To trigger a reconcile manually, touch a Node:

```bash
kubectl annotate node <node-name> test=1 --overwrite
```

You will see the reconciler log updated pod counts immediately.

To verify the index is doing real work, look at how fast reconciles complete — even in a cluster with hundreds of Pods, the list returns in microseconds because no API server round trip is needed.

## Key code walkthrough

**Index key constant** (`main.go:29`):
```go
const podNodeNameIndex = ".spec.nodeName"
```
Naming it as a constant prevents typos between the registration and use sites.

**Index registration** (`main.go:67-76`):
```go
mgr.GetFieldIndexer().IndexField(context.Background(),
    &corev1.Pod{},
    podNodeNameIndex,
    func(obj client.Object) []string {
        pod := obj.(*corev1.Pod)
        if pod.Spec.NodeName == "" {
            return nil
        }
        return []string{pod.Spec.NodeName}
    },
)
```
The `extractFn` returns `nil` for unscheduled Pods (no `nodeName` set yet). Returning `nil` excludes the object from the index entirely.

**Using the index in a List call** (`main.go:37-43`):
```go
podList := &corev1.PodList{}
r.List(ctx, podList,
    client.MatchingFields{podNodeNameIndex: req.Name},
)
logger.Info("pods on node", "node", req.Name, "count", len(podList.Items))
```
`client.MatchingFields` is an option that tells the cache client to use the registered index. Without this option, `r.List` returns all Pods (filtered only by any other options like `client.InNamespace`).

**Scheme setup** (`main.go:52-53`):
```go
s := runtime.NewScheme()
utilruntime.Must(corev1.AddToScheme(s))
```
The index is on `corev1.Pod` and the controller watches `corev1.Node`, so only the built-in core types need to be in the scheme — no custom CRD type is involved.

## References

- [controller-runtime client godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client)
- [FieldIndexer interface](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#FieldIndexer)
- [MatchingFields option](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client#MatchingFields)
- [upstream client/interfaces.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/interfaces.go)
- [upstream cache/informer_cache.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/cache/informer_cache.go)
