# Case 21: Standalone Webhook (No Manager)

Demonstrates how to build a webhook HTTP handler independently of a controller Manager, using `admission.WithCustomDefaulter` and standard `net/http` routing — the pattern for sidecar injectors, namespace-scoped webhook servers, and any scenario where a full Manager is unnecessary overhead.

## What this case teaches

- How `admission.WithCustomDefaulter` produces a `net/http.Handler` without any Manager
- How the untyped `admission.Defaulter[runtime.Object]` (aka `admission.CustomDefaulter`) differs from a concretely-typed `admission.Defaulter[*Widget]` — the untyped form receives `runtime.Object` and requires a type assertion
- How to wire webhook handlers into a standard `http.ServeMux`
- How `webhook.NewServer` provides TLS termination independently of a Manager
- When to use standalone webhooks versus Manager-integrated webhooks
- The pattern for sidecar injectors and other non-controller webhook use cases

## Key concepts

**Why build a webhook without a Manager?**
A Manager brings a lot: a cache, a reconciliation loop, leader election, health endpoints, metrics. For some webhook scenarios you need none of that. Examples:
- A sidecar injector that only mutates Pods — no reconciler, no CRD, no informer cache needed.
- A webhook deployed as a sidecar inside an application Pod.
- A webhook that only validates/defaults a single resource type owned by another component.
- Deployments where you want fine-grained control over the HTTP server (custom middleware, routing, timeouts).

Tying these to a Manager would couple them to the full controller lifecycle unnecessarily.

**Why instantiate the defaulter over `runtime.Object` instead of a concrete type?**
`admission` offers two constructors. The generic `WithDefaulter[*Widget]` gives compile-time type safety — `Default(ctx, *Widget)` — but only works when you commit to a concrete type (case 19 uses it through a Manager). The untyped path lets a single handler default any registered type, type-asserting inside `Default()`: handy for a generic standalone server where multiple handlers share one process without agreeing on a concrete type at compile time.

**Use `WithCustomDefaulter` for the untyped form, not `WithDefaulter[runtime.Object]`.**
It is tempting to write `WithDefaulter(scheme, defaulter)` and let `T` infer to `runtime.Object`. That compiles but **panics at request time**: `WithDefaulter[T]` constructs request objects via `reflect.TypeOf` on the zero value of `T`, which is `nil` for an interface, so `typ.Kind()` dereferences a nil `reflect.Type`. For the untyped case you must use `admission.WithCustomDefaulter(scheme, &Widget{}, defaulter)` — it takes an explicit prototype object it clones per request (via `DeepCopyObject`) instead of reflecting on the type. `admission.CustomDefaulter` is an alias for `Defaulter[runtime.Object]`, so the same `standaloneDefaulter` satisfies both.

**Why `net/http.ServeMux` instead of the Manager's webhook server?**
`http.ServeMux` is the standard Go HTTP multiplexer. Using it directly means you can add middleware (logging, auth, rate limiting), custom health endpoints, or multiple webhook paths with independent handlers — all with standard Go HTTP idioms. The Manager's webhook server is convenient but opinionated.

**Why `webhook.NewServer` for TLS even without a Manager?**
The Kubernetes API server will only call webhooks over HTTPS. `webhook.Server` handles TLS configuration, certificate loading, and optional certificate rotation. Using it standalone gives you the same TLS behaviour as the Manager-integrated cases without pulling in the rest of the Manager.

**What is the relationship between `webhook.Server` and `http.ServeMux`?**
`webhook.Server` wraps an `http.Handler`. You register your `ServeMux` on the server, and the server handles TLS. In production you would call `srv.Register("/mutate-widget", mutateHandler)` or pass the mux as the server's handler — the exact wiring depends on how you integrate the server with its `Start` method.

## How to run

### Verify the handler directly (recommended)

Because this handler needs no Manager, cluster, or certificates, the simplest
and most reliable check is a unit test that drives it in-process. `webhook_test.go`
POSTs an `AdmissionReview` to the handler and asserts the response — exactly the
request the API server would send:

```bash
cd 21-webhook-standalone
go test ./... -v
```

```
--- PASS: TestStandaloneDefaulter_PatchesEmptyColor
--- PASS: TestStandaloneDefaulter_KeepsExplicitColor
```

`TestStandaloneDefaulter_PatchesEmptyColor` sends a Widget with no color and
asserts the response is `allowed` with a JSON patch setting `/spec/color` to
`green`; the second test confirms an explicit color is left untouched. No
envtest or `KUBEBUILDER_ASSETS` needed — the handler is plain `http`.

### Running the wiring demo

The `main.go` entrypoint is intentionally illustrative — it builds the handler
and server but does not call `srv.Start()` (which needs valid certs).

```bash
go run ./...
```

Expected output:
```
level=INFO msg="standalone webhook server configured" addr=:9444/mutate-widget certDir=/tmp/webhook-certs
level=INFO msg="handler registered — integrate with your own TLS server to serve requests"
```

**To serve for real**, generate certs, register the handler on the server, and
call `srv.Start(ctx)`:

```bash
mkdir -p /tmp/webhook-certs
openssl req -x509 -newkey rsa:4096 -keyout /tmp/webhook-certs/tls.key \
  -out /tmp/webhook-certs/tls.crt -days 365 -nodes -subj "/CN=localhost"
export WEBHOOK_CERT_DIR=/tmp/webhook-certs
```

Then a `curl` against the running server (bypassing TLS verification with `-k`):
```bash
curl -X POST https://localhost:9444/mutate-widget \
  -H "Content-Type: application/json" -k \
  -d '{
    "apiVersion": "admission.k8s.io/v1",
    "kind": "AdmissionReview",
    "request": {
      "uid": "abc-123",
      "operation": "CREATE",
      "object": {
        "apiVersion": "example.com/v1alpha1",
        "kind": "Widget",
        "metadata": {"name": "test"},
        "spec": {"color": ""}
      }
    }
  }'
```

Expected response: an `AdmissionReview` with `response.allowed: true` and a JSON
patch setting `spec.color` to `"green"` — the same thing the test asserts.

## What to observe

- The program starts and exits immediately — the server wiring is shown but `srv.Start()` is not called in this illustrative case. In production the last lines would be `srv.Register("/mutate-widget", mutateHandler)` followed by `srv.Start(ctx)`.
- Port 9444 is used (not 9443) to avoid conflicting with cases 18–20 if both are running.
- The `WEBHOOK_CERT_DIR` environment variable defaults to `/tmp/webhook-certs` — no environment setup needed to observe the wiring.
- The `standaloneDefaulter.Default()` method requires a type assertion (`obj.(*Widget)`) because the untyped `Defaulter[runtime.Object]` receives `runtime.Object`. If the assertion fails (wrong type passed), it silently returns `nil` — in production you would return an error here.

## Key code walkthrough

**`main.go` lines 67–79 — untyped `Defaulter` with type assertion:**
```go
type standaloneDefaulter struct{}

func (d *standaloneDefaulter) Default(_ context.Context, obj runtime.Object) error {
    w, ok := obj.(*Widget)
    if !ok {
        return nil
    }
    if w.Spec.Color == "" {
        w.Spec.Color = "green"
        ctrl.Log.Info("standalone defaulter: set color to green", "name", w.Name)
    }
    return nil
}
```
`runtime.Object` instead of `*Widget`. The type assertion is the cost of handling the object untyped.

**`main.go` lines 94–101 — building the handler and wiring the mux:**
```go
mutateHandler := admission.WithCustomDefaulter(s, &Widget{}, &standaloneDefaulter{})

// Wire directly into net/http
mux := http.NewServeMux()
mux.Handle("/mutate-widget", mutateHandler)
mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
    _, _ = w.Write([]byte("ok"))
})
```
`WithCustomDefaulter` takes the scheme (for deserializing the incoming object), a prototype object (`&Widget{}`) it clones per request, and the `Defaulter`. The result is a plain `http.Handler` — use `WithCustomDefaulter` here rather than `WithDefaulter[runtime.Object]`, which panics (see Key concepts).

**`main.go` lines 108–112 — standalone TLS server:**
```go
srv := webhook.NewServer(webhook.Options{
    Port:    9444, // different port to avoid conflict with cases 18-20
    CertDir: certDir,
    Host:    "0.0.0.0",
})
```
`webhook.NewServer` is usable independently of a Manager. `Host: "0.0.0.0"` binds to all interfaces — appropriate for a server deployed in a Pod.

## References

- [controller-runtime webhook/admission godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/webhook/admission)
- [upstream source — WithCustomDefaulter](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/admission/defaulter_custom.go)
- [upstream source — webhook server](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/webhook/server.go)
- [net/http.ServeMux godoc](https://pkg.go.dev/net/http#ServeMux)
- [Kubernetes pod sidecar injection webhook example](https://kubernetes.io/blog/2019/03/21/a-guide-to-kubernetes-admission-controllers/)
