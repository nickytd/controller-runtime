# Case 05: Scheme and CRD

The Scheme is the runtime registry that maps between Go types and Kubernetes GroupVersionKinds (GVKs). Understanding it is a prerequisite for writing controllers for custom resources.

## What this case teaches

- How to define a minimal CRD Go type that satisfies `runtime.Object` (via `DeepCopyObject()`), with a correct deep copy that avoids aliasing the embedded `ObjectMeta` maps/slices and the list's backing array
- How `schema.GroupVersion` describes the API group and version for your custom resource
- How a `runtime.SchemeBuilder` collects types and produces an `AddToScheme` function
- How to build a fresh `runtime.Scheme` and populate it, then pass it to `manager.Options{Scheme: s}`
- How the typed client uses the scheme to serialize/deserialize custom resources
- The prerequisite of applying the CRD manifest before running the controller

## Key concepts

### What is the Scheme and why does it exist?

Kubernetes objects are exchanged over HTTP as JSON or Protobuf. The API server uses the URL (`/apis/example.com/v1alpha1/namespaces/default/widgets/my-widget`) to know the GVK, but the Go client receives a raw byte slice and needs to know which Go struct to decode it into. The `runtime.Scheme` is the lookup table: `GroupVersionKind → reflect.Type`. Without a registered type, the client cannot unmarshal the response and returns an error.

### What is `DeepCopyObject()` and why is it required?

`runtime.Object` requires two methods: `GetObjectKind() schema.ObjectKind` (satisfied by embedding `metav1.TypeMeta`) and `DeepCopyObject() runtime.Object`. The deep-copy method is called internally by the informer cache to store a clean copy of every object without aliasing. If your deep copy is shallow (a plain struct copy), nested slices, maps, and pointers will alias the cached object and you will see mysterious mutations.

This case follows the convention `controller-gen` uses: a `DeepCopyInto(out)` method does the field-by-field work, and `DeepCopy()`/`DeepCopyObject()` wrap it. `Widget`'s own `Spec` and `Status` are scalar, but it embeds `metav1.ObjectMeta`, which carries maps and slices (`Labels`, `Annotations`, `OwnerReferences`, `Finalizers`, `ManagedFields`) — so a plain struct copy would alias those. Its `DeepCopyInto` therefore delegates to `ObjectMeta.DeepCopyInto`. `WidgetList` holds `Items []Widget` — a plain struct copy would copy only the slice *header*, leaving the copy and the original sharing one backing array — so its `DeepCopyInto` allocates a fresh slice and deep-copies each element. This is precisely the aliasing bug a shallow copy hides, and why real projects let `controller-gen` generate these methods rather than hand-writing a struct copy.

### What is the `SchemeBuilder`?

`runtime.NewSchemeBuilder(funcs...)` returns a `runtime.SchemeBuilder` — a slice of functions that each mutate a `*runtime.Scheme`. Its `AddToScheme(s)` method runs them all in order, producing the conventional `AddToScheme(s *runtime.Scheme) error` entry point. The pattern is:

```
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(s *runtime.Scheme) error {
    s.AddKnownTypes(GroupVersion, &Widget{}, &WidgetList{})
    metav1.AddToGroupVersion(s, GroupVersion)
    return nil
}
```

`AddKnownTypes` wires each Go type to its GVK, and `metav1.AddToGroupVersion` registers the shared metav1 types (`ListOptions`, `DeleteOptions`, etc.) for the group so the client can issue list/watch/delete calls. This is the same pattern every `k8s.io/api` package uses.

> Earlier controller-runtime versions offered a `scheme.Builder{GroupVersion: gv}` helper with a `Register(...)` method. It is now deprecated in favor of the apimachinery builder shown above, because API-type packages should depend only on `apimachinery`, not on `controller-runtime`.

### Why must you apply the CRD manifest before running the controller?

The controller calls `List` and `Watch` against `widgets.example.com/v1alpha1`. If the CRD does not exist in the cluster, the API server returns a 404 "resource type not found" error and the informer fails to start. The Manager will log the error and exit. The CRD manifest tells the API server about the new resource type — it is the API registration step that must precede any controller.

### What does `manager.Options{Scheme: s}` do?

Without a custom scheme, the Manager uses `scheme.Scheme` from `k8s.io/client-go/kubernetes/scheme`, which only contains built-in Kubernetes types. Passing your custom scheme replaces this default, giving the manager's client and cache knowledge of your CRD types. The scheme is also used by `ctrl.SetControllerReference`, serialization, and REST mapper lookups.

## How to run

First, apply the CRD to your cluster:

```bash
cd 05-scheme-and-crd
kubectl apply -f widget_crd.yaml
```

Verify the CRD was registered:

```bash
kubectl get crd widgets.example.com
# NAME                    CREATED AT
# widgets.example.com     2026-...
```

Then run the controller:

```bash
go run ./...
```

In another terminal, create a Widget:

```bash
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: my-widget
  namespace: default
spec:
  color: blue
  size: 3
EOF
```

Clean up:

```bash
kubectl delete widget my-widget -n default
kubectl delete crd widgets.example.com
```

## What to observe

When you apply the Widget, the controller logs:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="reconciling Widget" color=blue size=3
```

The key observation is that the typed client successfully decoded the Widget from JSON into the `*Widget` Go struct — this is the scheme in action. Without the `AddToScheme(s)` call, the client would return `no kind is registered for the type` or fail to decode the response.

If you run the controller without applying the CRD first, you will see:

```
time=2024-01-15T10:23:01.234+01:00 level=ERROR msg="failed to get API group resources" error="...widgets.example.com not found..."
```

## Key code walkthrough

**GVK and SchemeBuilder** (`main.go:32-47`):
```go
var GroupVersion = schema.GroupVersion{Group: "example.com", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(s *runtime.Scheme) error {
    s.AddKnownTypes(GroupVersion, &Widget{}, &WidgetList{})
    metav1.AddToGroupVersion(s, GroupVersion)
    return nil
}
```
Registering both the singular and list type is mandatory. The client uses `WidgetList` when you call `r.List(ctx, &WidgetList{}, ...)`.

**DeepCopyObject** (`main.go:77-121`):
```go
// Items must be copied element by element — a plain struct assignment
// would alias the backing array.
func (w *WidgetList) DeepCopyInto(out *WidgetList) {
    *out = *w
    out.TypeMeta = w.TypeMeta
    w.ListMeta.DeepCopyInto(&out.ListMeta)
    if w.Items != nil {
        out.Items = make([]Widget, len(w.Items))
        for i := range w.Items {
            w.Items[i].DeepCopyInto(&out.Items[i])
        }
    }
}

func (w *WidgetList) DeepCopyObject() runtime.Object { return w.DeepCopy() }
```
`Widget`'s `DeepCopyInto` copies the struct and then delegates to `ObjectMeta.DeepCopyInto` — the embedded `metav1.ObjectMeta` holds maps and slices that a plain `*out = *w` would alias. `WidgetList` additionally allocates a fresh `Items` slice, otherwise the copy would share the original's backing array. `controller-gen` generates exactly this shape for you in real projects.

**Building the scheme and passing to Manager** (`main.go:148-159`):
```go
s := runtime.NewScheme()
if err := AddToScheme(s); err != nil { ... }
mgr, err := manager.New(cfg, manager.Options{Scheme: s, ...})
```

## References

- [runtime.Scheme godoc](https://pkg.go.dev/k8s.io/apimachinery/pkg/runtime#Scheme)
- [runtime.SchemeBuilder godoc](https://pkg.go.dev/k8s.io/apimachinery/pkg/runtime#SchemeBuilder)
- [upstream source: pkg/scheme/scheme.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/scheme/scheme.go)
- [upstream source: pkg/manager/manager.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/manager/manager.go)
- [Kubernetes CRD documentation](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/)
- [controller-gen deep copy generation](https://book.kubebuilder.io/reference/controller-gen)
