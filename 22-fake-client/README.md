# Case 22: Unit Testing with the Fake Client

Demonstrates how to unit-test a controller reconciler without a real Kubernetes cluster or a running Manager, using controller-runtime's in-memory fake client to assert that the reconciler creates the correct child objects and updates status correctly.

## What this case teaches

- How `fake.NewClientBuilder().WithObjects().Build()` seeds an in-memory store with initial objects
- Why `WithStatusSubresource()` is required to make `r.Status().Update()` persist in tests
- How to call a reconciler directly (bypassing the Manager) and assert side effects
- How to test the not-found path (reconciler receives a request for a deleted resource)
- How the fake client implements the full `client.Client` interface so reconcilers need no changes

## Key concepts

**Why a fake client instead of mocking the `client.Client` interface?**
`client.Client` has ~15 methods. Hand-writing a mock for every test is tedious and fragile — the mock drifts from the real API. The fake client is a real, in-memory implementation of the full interface maintained by the controller-runtime project. It behaves like the real API server for CRUD operations, including proper error semantics (not-found, already-exists). You get correct behaviour without any mocking framework.

**Why `WithObjects(widget)` in the builder?**
The reconciler's first action is `r.Get(ctx, req.NamespacedName, widget)`. If the object does not exist in the fake store, it returns a not-found error. `WithObjects` seeds the store before the test runs, simulating the state that would exist in a real cluster at the point the reconciler is triggered.

**Why is `WithStatusSubresource(&Widget{})` required?**
Kubernetes separates `.status` from the rest of the object via the status subresource. In a real cluster, `r.Status().Update()` makes a PATCH to `/apis/.../widgets/name/status` — a distinct endpoint that only updates the status fields. In the fake client, status writes are no-ops by default (to match the behaviour of a resource that does not have a status subresource enabled). Calling `WithStatusSubresource(&Widget{})` tells the fake client: "treat Widget as having a status subresource" — after which `r.Status().Update()` actually persists the status fields.

Without this, `widget.Status.Ready = true` followed by `r.Status().Update(ctx, widget)` would succeed but leave the stored object with `Status.Ready = false`. The test would then incorrectly fail.

**Why call the reconciler directly instead of starting a Manager?**
Unit tests should be fast (milliseconds), deterministic, and isolated. Starting a Manager brings up goroutines, an informer cache, and a work queue — all of which introduce timing dependencies. Calling `r.Reconcile(ctx, req)` directly is synchronous: you call it, it returns, you assert. There are no goroutines, no waits, no flaky timing.

**Why is `checkReconcileNotFound` a useful test?**
Controllers frequently handle the case where an object is deleted between when the reconcile request was enqueued and when it runs. The correct behaviour is to return `nil, nil` (ignore not-found). Testing this explicitly verifies that your reconciler handles `client.IgnoreNotFound(err)` correctly and does not crash or re-enqueue on a missing resource.

**Why is this a `main()` program instead of `_test.go` tests?**
This repo uses `go run ./...` as a uniform "run any case" command, so each case is a runnable `package main`. The three checks are plain functions that return an `error`; `main()` runs them in sequence, logs each result via `slog`, and calls `os.Exit(1)` on the first failure. This keeps the fake-client demonstration runnable with the same command as every other case, without a `go test` harness. In a real project these would be `_test.go` functions taking `*testing.T` — the error-returning shape here is a convenience specific to this learning repo, not a production pattern.

## How to run

No cluster, no kubeconfig, no environment setup required.

```bash
cd 22-fake-client
go run ./...
```

Expected output:
```
2026/10/12 22:49:37 INFO check passed name=ReconcileCreatesConfigMap configMap=my-widget-config color=red
2026/10/12 22:49:37 INFO check passed name=ReconcileSetsStatusReady widget=my-widget ready=true
2026/10/12 22:49:37 INFO check passed name=ReconcileNotFound
2026/10/12 22:49:37 INFO all checks passed
```

The program exits 0 when all checks pass and 1 (logging `check failed`) on the first failure.

## What to observe

- All three checks complete in milliseconds — no network, no cluster, no sleep.
- `checkReconcileCreatesConfigMap`: after reconcile, the ConfigMap `my-widget-config` exists in the fake store with `data.color = "red"`. Read it back with `fakeClient.Get()`.
- `checkReconcileSetsStatusReady`: after reconcile, the Widget's status has `Ready: true`. Read it back with `fakeClient.Get()`. This only works because `WithStatusSubresource` was set.
- `checkReconcileNotFound`: no Widget is seeded. Reconcile is called with `name: "missing"`. The reconciler calls `client.IgnoreNotFound(err)` and returns `nil, nil`. No panic, no error.
- Remove `WithStatusSubresource(&Widget{})` from one check and observe that `Status.Ready` remains `false` after reconcile — this demonstrates why the call is necessary.

## Key code walkthrough

**`main.go` lines 116–120 — building the fake client:**
```go
fakeClient := fake.NewClientBuilder().
    WithScheme(s).
    WithObjects(widget).
    WithStatusSubresource(&Widget{}).
    Build()
```
`WithScheme` teaches the fake client how to serialize/deserialize objects. `WithObjects` seeds the store. `WithStatusSubresource` enables status writes.

**`main.go` lines 122–124 — calling the reconciler directly:**
```go
r := &WidgetReconciler{Client: fakeClient}
_, err := r.Reconcile(context.Background(), reconcile.Request{
    Name: "my-widget", Namespace: "default",
})
```
No Manager, no goroutines. The reconciler is just a struct with a `Reconcile` method. Inject the fake client, call the method, check the result.

**`main.go` lines 131–137 — asserting side effects:**
```go
cm := &corev1.ConfigMap{}
if err := fakeClient.Get(context.Background(),
    types.NamespacedName{Name: "my-widget-config", Namespace: "default"}, cm); err != nil {
    return fmt.Errorf("expected ConfigMap to exist: %w", err)
}
if cm.Data["color"] != "red" {
    return fmt.Errorf("expected color=red, got %s", cm.Data["color"])
}
```
After reconcile, read the ConfigMap from the same fake client instance. The reconciler used this client to create it, so it is present in the in-memory store.

**`main.go` lines 178–182 — not-found test:**
```go
fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
// No WithObjects — store is empty
r := &WidgetReconciler{Client: fakeClient}
_, err := r.Reconcile(context.Background(), reconcile.Request{
    Name: "missing", Namespace: "default",
})
if err != nil {
    return fmt.Errorf("expected no error on not-found, got: %w", err)
}
```
Empty fake store. The reconciler's `r.Get()` returns not-found, `client.IgnoreNotFound(err)` returns `nil`, reconcile returns `nil, nil`.

## References

- [controller-runtime client/fake godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client/fake)
- [upstream source — fake client builder](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/client/fake/client.go)
- [controller-runtime client godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client)
- [Go testing package godoc](https://pkg.go.dev/testing)
