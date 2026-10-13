# Case 20: Conversion Webhook (Multi-Version CRD)

Demonstrates how to implement a CRD conversion webhook using controller-runtime's hub-spoke pattern, enabling a Kubernetes API to serve multiple API versions simultaneously while storing objects in a single canonical version.

## What this case teaches

- The hub-spoke conversion pattern: one central version (hub) that all others convert through
- How `conversion.Hub` marks the storage version with a single marker method `Hub()`
- How `conversion.Convertible` works: `ConvertTo` (spoke → hub) and `ConvertFrom` (hub → spoke)
- How to handle field renames across versions (e.g., `size` → `magnitude`)
- How `ctrl.NewWebhookManagedBy[T]` registers a conversion webhook for a spoke type
- Why the CRD must declare `conversion.strategy: Webhook`
- Why every version must be registered under the **same Kind** so the API server treats them as one resource
- The scalability advantage of hub-spoke over pairwise conversion

## Key concepts

**Why have multiple CRD versions at all?**
APIs evolve. You might rename a field, restructure the spec, or promote a field from optional to required. Kubernetes allows a CRD to expose multiple versions simultaneously via `spec.versions`. Clients pinned to an older version continue to work while new clients use the newer API. The API server needs a way to convert between versions on the fly — that is what the conversion webhook provides.

**Why hub-spoke instead of pairwise conversion?**
With N versions, pairwise conversion requires N*(N-1)/2 converters (for 4 versions: 6 converters, for 5 versions: 10). Hub-spoke requires only N-1 converters — each spoke implements one `ConvertTo` and one `ConvertFrom`. The hub version is the single intermediate all conversions flow through. This is the same pattern used by Kubernetes itself for its own internal type conversion.

**Why must both versions register under the same Kind?**
The two Go types have different names (`Widget`, `WidgetV1Beta1`), but they are two *versions of one resource*. In the scheme they must both register as Kind `Widget` — differing only in version:
```go
s.AddKnownTypes(GVv1alpha1, &Widget{}, ...)                       // v1alpha1, Kind=Widget
s.AddKnownTypeWithName(GVv1beta1.WithKind("Widget"), &WidgetV1Beta1{})  // v1beta1, Kind=Widget
```
`AddKnownTypes` would derive the Kind from the Go type name (`WidgetV1Beta1`), giving the spoke a *different* GroupKind from the hub. The conversion machinery groups versions by GroupKind to decide whether a type is convertible — if the two versions don't share a Kind, it sees a single-version resource and silently never enables the webhook. This is an easy mistake to make and produces no error, just no conversion.

**Why does the hub implement `Hub()` as an empty method?**
It is a compile-time marker that satisfies the `conversion.Hub` interface. The framework uses this to determine which version is the storage version. The method has no body because its purpose is purely to carry type information — it is the Go equivalent of a tag.

**Why does only the spoke implement `ConvertTo`/`ConvertFrom`?**
The hub is the fixed point of the conversion graph. Every spoke knows how to translate itself to/from the hub. The hub does not need to know about any specific spoke — it just needs to be convertible to/from the common format. Adding a new version means only implementing two new methods, not modifying any existing code.

**What does "spoke → hub" mean in practice?**
When a client requests a Widget in `v1beta1` but the object is stored in `v1alpha1` (the hub), the API server asks the conversion webhook to `ConvertFrom` the stored `v1alpha1` into `v1beta1`. When a client creates a Widget in `v1beta1`, the API server asks the webhook to `ConvertTo` hub (`v1alpha1`) before storing it.

**Why must `ObjectMeta` be copied?**
`ObjectMeta` contains the name, namespace, labels, annotations, resource version, and owner references. These are version-agnostic and must be preserved verbatim in every conversion. Forgetting to copy `ObjectMeta` would cause the converted object to lose its identity.

## How to run

A conversion webhook only runs if the CRD declares `conversion.strategy: Webhook`
**and** the API server can reach the webhook over trusted TLS. Running `go run`
alone leaves the conversion unexercised, and on kind/minikube the apiserver can't
reach a host process at all.

### Verify it end-to-end with envtest (recommended)

The self-contained test in `webhook_test.go` is the reliable way to see the
conversion actually happen. envtest stands up a real apiserver, installs the
multi-version CRD, detects that the scheme's types are convertible, and rewrites
the CRD's conversion-webhook `clientConfig` to point at the manager's local
webhook server (injecting the caBundle).

```bash
cd 20-webhook-conversion
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
go test ./... -v
```

You should see:
```
--- PASS: TestConversion_SpokeToHub
--- PASS: TestConversion_HubToSpoke
```

`TestConversion_SpokeToHub` creates a Widget via `v1beta1` (`magnitude: 7`) and
reads it back as `v1alpha1` (`size: 7`); `TestConversion_HubToSpoke` does the
reverse. Both pass only because the API server actually invoked the conversion
webhook. (See case 23 for envtest fundamentals.)

### Running against a real cluster (and why `go run` alone isn't enough)

```bash
kubectl apply -f widget_crd.yaml          # declares both versions + conversion strategy
export WEBHOOK_CERT_DIR=/tmp/webhook-certs # must contain tls.crt + tls.key
go run ./...
# -> "conversion webhook registered — v1alpha1 (hub) ↔ v1beta1 (spoke)"
```

As with cases 18–19, on kind/minikube the apiserver (in a container/VM) can't
reach a `go run` process on your host without extra networking — the envtest path
above is the dependable way to exercise this. Once registered, round-trips work
both directions:

```bash
# Create via the hub (v1alpha1), read back as v1beta1 — size appears as magnitude
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: my-widget
spec:
  color: red
  size: 7
EOF
kubectl get widget my-widget -o yaml --api-version=example.com/v1beta1
```

```yaml
apiVersion: example.com/v1beta1
kind: Widget
metadata:
  name: my-widget
spec:
  color: red
  magnitude: 7   # converted from "size"
```

Creating via `v1beta1` (`magnitude`) and reading back as `v1alpha1` (`size`)
works symmetrically.

## What to observe

- The API server transparently converts between versions. A client using `v1alpha1` and a client using `v1beta1` both see a consistent view of the same stored object.
- The conversion webhook receives a `ConversionReview` object (not an `AdmissionReview`). The framework handles this protocol difference automatically.
- `ObjectMeta` is preserved exactly during both directions of conversion — labels, annotations, and owner references survive round-trips.
- Only the `WidgetV1Beta1` spoke implements `ConvertTo`/`ConvertFrom`. The hub `Widget` only implements the empty `Hub()` method. This asymmetry is intentional.
- Adding a third version `v1gamma1` would only require implementing `ConvertTo`/`ConvertFrom` on the new type — no changes to `Widget` or `WidgetV1Beta1`.

## Key code walkthrough

**`main.go` line 77 — hub marker:**
```go
func (w *Widget) Hub() {}
```
Empty method body. Satisfies `conversion.Hub`. Tells controller-runtime: "v1alpha1 is the storage version; all conversions pass through here."

**`main.go` lines 95–96 — registering the spoke under Kind "Widget":**
```go
s.AddKnownTypeWithName(GVv1beta1.WithKind("Widget"), &WidgetV1Beta1{})
s.AddKnownTypeWithName(GVv1beta1.WithKind("WidgetList"), &WidgetV1Beta1List{})
```
Both versions share GroupKind `example.com/Widget`, differing only by version. Using `AddKnownTypes` would register Kind `WidgetV1Beta1` and the conversion webhook would silently never enable (see Key concepts).

**`main.go` lines 108–111 — field rename in the spoke:**
```go
type WidgetV1Beta1Spec struct {
    Color     string `json:"color"`
    Magnitude int    `json:"magnitude"` // renamed from "size"
}
```
The rename is expressed entirely in the Go struct. The conversion methods map between the two field names.

**`main.go` lines 134–140 — ConvertTo (spoke → hub):**
```go
func (src *WidgetV1Beta1) ConvertTo(dstRaw conversion.Hub) error {
    dst := dstRaw.(*Widget)
    dst.ObjectMeta = src.ObjectMeta
    dst.Spec.Color = src.Spec.Color
    dst.Spec.Size = src.Spec.Magnitude // rename back
    return nil
}
```
Note the signature: `conversion.Convertible` in this version takes just the `conversion.Hub` argument (no `context.Context`). A mismatched signature means the type silently fails to satisfy the interface and the webhook never registers.

**`main.go` lines 143–149 — ConvertFrom (hub → spoke):**
```go
func (dst *WidgetV1Beta1) ConvertFrom(srcRaw conversion.Hub) error {
    src := srcRaw.(*Widget)
    dst.ObjectMeta = src.ObjectMeta
    dst.Spec.Color = src.Spec.Color
    dst.Spec.Magnitude = src.Spec.Size // rename
    return nil
}
```

**`main.go` lines 181–183 — registering the conversion webhook:**
```go
if err := ctrl.NewWebhookManagedBy(mgr, &WidgetV1Beta1{}).
    Complete(); err != nil {
    setupLog.Error(err, "unable to set up webhook")
```
No `.WithValidator()` or `.WithDefaulter()` — just `.Complete()`. The framework detects that `WidgetV1Beta1` implements `conversion.Convertible` and registers the conversion endpoint automatically.

## References

- [controller-runtime conversion godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/conversion)
- [upstream source — conversion](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/conversion/conversion.go)
- [upstream source — conversion webhook handler](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/conversion/conversion.go)
- [Kubernetes CRD versioning reference](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definition-versioning/)
- [Kubebuilder book — conversion webhooks](https://book.kubebuilder.io/multiversion-tutorial/conversion.html)
