# Case 18: Validating Admission Webhook

Demonstrates how to implement a Kubernetes validating admission webhook using controller-runtime's typed `admission.Validator[T]` generic interface, which rejects objects that fail business-logic constraints before they are persisted.

## What this case teaches

- How `admission.Validator[T]` separates create, update, and delete validation into distinct methods
- How `field.ErrorList` builds structured, field-level error messages that clients and `kubectl` can display precisely
- How `ctrl.NewWebhookManagedBy[T]` registers the webhook handler and wires it into the Manager lifecycle
- How the webhook server is configured with TLS via `webhook.NewServer` and `webhook.Options`
- How webhook paths are auto-generated from the GVK (`/validate-<group>-<version>-<kind>`)
- How to supply the certificate directory via an environment variable
- Why a `ValidatingWebhookConfiguration` is required for the webhook to fire at all — and how `webhook_config.yaml` plus the envtest in `webhook_test.go` make that verifiable

## Key concepts

**Why validating webhooks instead of admission controllers built into the API server?**
Built-in admission controllers are compiled into the API server and cannot be extended. Validating webhooks run as your own HTTP server and are called by the API server for every create/update/delete matching a `ValidatingWebhookConfiguration`. This means you can enforce arbitrary business rules — cross-field constraints, external lookups, org-policy checks — without changing the API server.

**Why `admission.Validator[T]` rather than a raw handler?**
The generic interface handles all the HTTP boilerplate: deserializing the `AdmissionReview` request, extracting the typed object (and old object for updates), calling your method, and serializing the response. You only implement the validation logic. Without the interface you'd write ~100 lines of handler code for every webhook.

**Why `field.ErrorList` and `field.Invalid`?**
`field.ErrorList` produces structured errors that include the JSON path of the offending field, the bad value, and a human message. `ToAggregate()` collects them into one error, which the webhook returns as a single denial. `kubectl` surfaces all of them together:
```
admission webhook "validate.widget.example.com" denied the request:
[spec.color: Invalid value: "purple": must be one of: red, green, blue,
spec.size: Invalid value: 0: must be between 1 and 10]
```
A plain `errors.New("bad color")` would only show the message string with no field context.

(Note: a webhook *denial* comes back as a `Forbidden`-status API error, not the `Invalid`/422 status the apiserver uses for its own schema validation — `field.Invalid` here names the *kind of field problem*, not the HTTP status.)

**Why `ValidateDelete` returns `nil, nil` here?**
Delete validation is rarely needed for most resources. The interface requires you to implement it, but returning `nil, nil` means "allow all deletes". You can use it to block deletion of resources that are still referenced by others.

**Why separate create and update methods?**
Some constraints only apply on creation (e.g., an immutable field). On update you receive both the old and new object, which lets you enforce immutability: if `newObj.Spec.SomeField != oldObj.Spec.SomeField { return field.Forbidden(...) }`.

**Why auto-generated webhook paths?**
controller-runtime derives the path deterministically from the GVK: `/validate-<group-with-dots-replaced-by-dashes>-<version>-<kind-lowercased>`. Your `ValidatingWebhookConfiguration` in the cluster must use the exact same path. Using the generated path avoids mismatches.

## How to run

A validating webhook only fires if two things are true: the webhook server is
running with TLS, **and** a `ValidatingWebhookConfiguration` tells the API server
to call it. Miss the second and the API server never consults your webhook — it
writes straight to etcd and your "invalid" object is accepted. That is the most
common first-time surprise with webhooks.

### Verify it end-to-end with envtest (recommended)

The self-contained test in `webhook_test.go` is the reliable way to see the
webhook actually reject and accept objects. envtest stands up a real apiserver,
installs the CRD and the `ValidatingWebhookConfiguration` from
`webhook_config.yaml`, generates serving certs, and rewrites the webhook's
`clientConfig` to point at the manager's local webhook server — all the wiring
the manual path below is fiddly to reproduce.

```bash
cd 18-webhook-validating
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
go test ./... -v
```

You should see:
```
--- PASS: TestWebhook_RejectsInvalidWidget
--- PASS: TestWebhook_AcceptsValidWidget
```

`TestWebhook_RejectsInvalidWidget` passes only because the API server genuinely
invokes the webhook: the invalid Widget (`color: purple`, `size: 0`) comes back
denied with both `spec.color` and `spec.size` errors. The valid Widget is
persisted. (See case 23 for envtest fundamentals.)

### Running against a real cluster (and why `go run` alone isn't enough)

```bash
kubectl apply -f widget_crd.yaml
export WEBHOOK_CERT_DIR=/tmp/webhook-certs   # must contain tls.crt + tls.key
go run ./...
# -> "validating webhook registered at /validate-example-com-v1alpha1-widget"
```

Starting the server is **not sufficient on its own**. Until you also create a
`ValidatingWebhookConfiguration` (see `webhook_config.yaml` for the shape), the
API server does not know the webhook exists, so this invalid Widget is written
straight to etcd instead of being rejected:

```bash
kubectl apply -f - <<EOF
apiVersion: example.com/v1alpha1
kind: Widget
metadata:
  name: bad-widget
spec:
  color: purple
  size: 0
EOF
kubectl get widget bad-widget   # accepted if the webhook is unregistered
```

As with case 19, on **kind**/**minikube** the apiserver runs in a container/VM
and cannot reach a `go run` process on your host without extra networking — which
is why the envtest path above is the dependable way to exercise this. Once the
configuration is correctly registered, the same `kubectl apply` is denied:

```
admission webhook "validate.widget.example.com" denied the request:
[spec.color: Invalid value: "purple": must be one of: red, green, blue,
spec.size: Invalid value: 0: must be between 1 and 10]
```

A Widget with valid values (`color: red`, `size: 5`) is accepted and persisted.

## Key code walkthrough

**`main.go` lines 64–81 — the Validator implementation:**
```go
type WidgetValidator struct{}

func (v *WidgetValidator) ValidateCreate(_ context.Context, obj *Widget) (admission.Warnings, error) {
    return v.validate(obj)
}
func (v *WidgetValidator) ValidateUpdate(_ context.Context, _, newObj *Widget) (admission.Warnings, error) {
    return v.validate(newObj)
}
func (v *WidgetValidator) ValidateDelete(_ context.Context, _ *Widget) (admission.Warnings, error) {
    return nil, nil
}
```
Three distinct entry points. `ValidateUpdate` receives the old object as the second parameter — useful for immutability checks.

**`main.go` lines 88–102 — structured field errors:**
```go
errs = append(errs, field.Invalid(
    field.NewPath("spec", "color"), w.Spec.Color,
    "must be one of: red, green, blue",
))
// ...
if len(errs) > 0 {
    return nil, errs.ToAggregate()
}
```
`field.NewPath("spec", "color")` constructs the JSON path `.spec.color`. The bad value is included so the error message is self-contained.

**`main.go` line 129 — webhook registration:**
```go
ctrl.NewWebhookManagedBy[*Widget](mgr, &Widget{}).
    WithValidator(&WidgetValidator{}).
    Complete()
```
The generic type parameter `[*Widget]` tells the framework which type to deserialize from the admission request. The path `/validate-example-com-v1alpha1-widget` is derived from the Widget's GVK at registration time. This registers the *handler* inside your process — it does **not** create the cluster-side `ValidatingWebhookConfiguration`; that is a separate object (below).

**`webhook_config.yaml` — telling the API server to call the webhook:**
```yaml
clientConfig:
  service:
    name: webhook-service
    namespace: default
    path: /validate-example-com-v1alpha1-widget   # must match the generated path
```
Without this object in the cluster, the handler is never invoked. The `path` must match the GVK-derived path above. envtest reads `clientConfig.service.path`, rewrites it to a local `url:` with the serving host/port, and injects the matching `caBundle` — which is why the test can exercise the webhook without a real Service.

**`main.go` lines 117–120 — TLS server configuration:**
```go
WebhookServer: webhook.NewServer(webhook.Options{
    Port:    9443,
    CertDir: os.Getenv("WEBHOOK_CERT_DIR"),
}),
```
The certificate directory must contain `tls.crt` and `tls.key`. cert-manager can inject these automatically in production; the envtest test points `CertDir` at the serving certs envtest generates.

## References

- [controller-runtime webhook/admission godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/webhook/admission)
- [controller-runtime webhook godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/webhook)
- [upstream source — admission](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/admission/validator.go)
- [upstream source — webhook server](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/server.go)
- [k8s.io/apimachinery field godoc](https://pkg.go.dev/k8s.io/apimachinery/pkg/util/validation/field)
- [Kubernetes admission webhooks reference](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
