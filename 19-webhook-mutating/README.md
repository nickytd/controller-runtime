# Case 19: Mutating Admission Webhook (Defaulter)

Demonstrates how to implement a Kubernetes mutating admission webhook using controller-runtime's typed `admission.Defaulter[T]` generic interface, which fills in missing fields and stamps metadata before an object is persisted.

## What this case teaches

- How `admission.Defaulter[T]` works: mutate the object in-place, the framework produces the JSON patch
- How to set default field values when the user omits them
- How to stamp metadata labels on every admitted object
- How `ctrl.NewWebhookManagedBy[T].WithDefaulter()` registers the mutating webhook
- How mutating webhooks run before validating webhooks in the Kubernetes admission chain
- The auto-generated mutating path: `/mutate-<group>-<version>-<kind>`

## Key concepts

**Why mutating webhooks?**
Kubernetes allows optional fields in CRD specs for usability — users should not need to specify every field. Mutating webhooks fill in safe defaults before the object is validated or stored. This keeps the API ergonomic while ensuring objects in etcd are always fully populated. Without a defaulter, your reconciler must handle every combination of missing fields defensively.

**Why `admission.Defaulter[T]` instead of setting defaults in the reconciler?**
If you default in the reconciler, the object in etcd lacks those defaults. Other components that read the object (e.g., `kubectl get`, another controller, a UI) see incomplete data. Defaulting at admission time means the persisted object always reflects the intended state. The user can also see what was defaulted immediately via `kubectl get`.

**Why does the framework produce the JSON patch, not you?**
The Kubernetes admission API requires the mutation to be expressed as a [JSON Patch (RFC 6902)](https://datatracker.ietf.org/doc/html/rfc6902) document. The framework takes a snapshot of the object before calling `Default()`, then diffs the before and after state to produce the patch automatically. You just mutate the Go struct and return `nil`.

**Why do mutating webhooks run before validating webhooks?**
Kubernetes guarantees this ordering in its admission chain. This means the validator in case 18 will see the defaulted values — so a Widget submitted without `spec.color` will have `color: "blue"` injected by the time the validator checks it. You can design validators that rely on defaulters having already run.

**Why add a `defaulted-by` label?**
Stamping metadata is a common production pattern. It makes it easy to audit which objects were touched by which defaulter (`kubectl get widgets -l defaulted-by=widget-defaulter`), useful when debugging or when you need to migrate the label scheme.

## How to run

A mutating webhook only runs if two things are true: the webhook server is
running with TLS, **and** a `MutatingWebhookConfiguration` tells the API server
to call it. Miss the second and the API server never consults your webhook — the
object is stored with its fields left unset, and no defaulting happens.

### Verify it end-to-end with envtest (recommended)

The self-contained test in `webhook_test.go` is the reliable way to see the
defaulter actually mutate objects. envtest stands up a real apiserver, installs
the CRD and the `MutatingWebhookConfiguration` from `webhook_config.yaml`,
generates serving certs, and rewrites the webhook's `clientConfig` to point at
the manager's local webhook server.

```bash
cd 19-webhook-mutating
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
go test ./... -v
```

You should see:
```
--- PASS: TestDefaulter_SetsMissingFields
--- PASS: TestDefaulter_KeepsExplicitValues
```

`TestDefaulter_SetsMissingFields` creates a Widget with no `color`/`size` and
reads it back with `color: blue`, `size: 5`, and the `defaulted-by` label —
proof the API server invoked the webhook. `TestDefaulter_KeepsExplicitValues`
confirms the defaulter only fills *unset* fields. (See case 23 for envtest
fundamentals.)

### Running against a real cluster (and why `go run` alone isn't enough)

```bash
kubectl apply -f widget_crd.yaml
export WEBHOOK_CERT_DIR=/tmp/webhook-certs   # must contain tls.crt + tls.key
go run ./...
# -> "mutating webhook registered at /mutate-example-com-v1alpha1-widget"
```

Starting the server is **not sufficient on its own**. Until you also create a
`MutatingWebhookConfiguration` (see `webhook_config.yaml` for the shape), the API
server does not know the webhook exists, so this Widget is stored *without*
defaults applied:

```bash
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: minimal-widget
spec: {}
EOF
kubectl get widget minimal-widget -o yaml   # color/size still empty if unregistered
```

As with case 18, on **kind**/**minikube** the apiserver runs in a container/VM
and cannot reach a `go run` process on your host without extra networking — which
is why the envtest path above is the dependable way to exercise this. Once the
configuration is correctly registered, the stored object carries the defaults:

```yaml
metadata:
  labels:
    defaulted-by: widget-defaulter
  name: minimal-widget
spec:
  color: blue    # defaulted from ""
  size: 5        # defaulted from 0
```

A Widget submitted with `color: red` keeps it, and only `size` and the label are
defaulted.

## What to observe

- The object returned by `kubectl get` shows the defaulted values — they were written to etcd by the API server after applying the JSON patch.
- The `defaulted-by: widget-defaulter` label appears on every Widget, regardless of what the user submitted.
- The webhook server logs which fields were defaulted, e.g.:
  ```
  time=2024-01-15T10:23:01.234+01:00 level=INFO msg="defaulted color" name=minimal-widget
  time=2024-01-15T10:23:01.235+01:00 level=INFO msg="defaulted size" name=minimal-widget
  ```
- If you combine this with case 18 (both webhooks registered), create a Widget with no spec — the defaulter runs first setting `color: blue` and `size: 5`, then the validator runs and accepts the now-valid object.
- Try creating a Widget that already has valid values — the `Default()` method skips those fields (`if w.Spec.Color == ""`).

## Key code walkthrough

**`main.go` lines 69–84 — the Defaulter implementation:**
```go
func (d *WidgetDefaulter) Default(_ context.Context, w *Widget) error {
    // logging elided
    if w.Spec.Color == "" {
        w.Spec.Color = "blue"
    }
    if w.Spec.Size == 0 {
        w.Spec.Size = 5
    }
    if w.Labels == nil {
        w.Labels = map[string]string{}
    }
    w.Labels["defaulted-by"] = "widget-defaulter"
    return nil
}
```
Mutate the struct in-place. Return `nil` on success. The framework diffs the object against its pre-call snapshot and generates the JSON patch automatically.

**`main.go` line 110 — webhook registration:**
```go
ctrl.NewWebhookManagedBy[*Widget](mgr, &Widget{}).
    WithDefaulter(&WidgetDefaulter{}).
    Complete()
```
`WithDefaulter` registers the handler at `/mutate-example-com-v1alpha1-widget`. Compare with case 18 which uses `WithValidator` to register at `/validate-...`. This registers the *handler* in your process — it does not create the cluster-side `MutatingWebhookConfiguration`; that is a separate object (`webhook_config.yaml`), whose `clientConfig.service.path` must match this generated path.

## References

- [controller-runtime webhook/admission godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/webhook/admission)
- [upstream source — defaulter](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/admission/defaulter.go)
- [upstream source — webhook builder](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/builder/webhook.go)
- [JSON Patch RFC 6902](https://datatracker.ietf.org/doc/html/rfc6902)
- [Kubernetes admission chain ordering](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#what-are-admission-webhooks)
