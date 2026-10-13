# Case 23: Integration Testing with envtest

Demonstrates how to write integration tests for a controller reconciler using controller-runtime's `envtest` package, which spins up a real `etcd` and `kube-apiserver` process — providing full API server semantics without a real cluster.

## What this case teaches

- How `envtest.Environment` starts real `etcd` and `kube-apiserver` binaries and returns a `*rest.Config`
- How `envtest.CRDInstallOptions` loads CRD YAML files before tests run
- How to start a full Manager with a real reconciler inside a test goroutine
- How `TestMain` orchestrates suite setup (start env → register scheme → start manager → run tests → teardown)
- How to poll with a deadline instead of sleeping fixed durations
- The difference between fake client tests (case 22) and envtest tests in terms of fidelity and cost
- How `KUBEBUILDER_ASSETS` and `setup-envtest` provide the binary assets

## Key concepts

**Why envtest instead of a fake client?**
The fake client (case 22) is fast and isolated, but it does not exercise: webhook admission, watch events from the real API server, owner reference garbage collection, status subresource enforcement, RBAC, or any other API server behaviour. `envtest` runs real binaries, so your tests exercise the actual reconciler lifecycle — the Manager starts, informers sync, the reconciler is triggered by real watch events, and status is written through the real status subresource endpoint. This gives you confidence that the reconciler works correctly in a real cluster.

**Why not use a real cluster for tests?**
Real clusters are slow to provision, require cleanup, cannot be parallelised safely across CI jobs, and may have pre-existing state that interferes. `envtest` starts fresh for every test run, is self-contained, and takes ~2–3 seconds to start. It is the standard approach for controller integration tests in the Kubernetes ecosystem.

**Why `TestMain` for setup?**
`TestMain` runs once per package, before any `Test*` functions. It is the correct place to start long-running infrastructure (the API server, the manager) that should persist across all tests in the suite. Individual `Test*` functions then create objects and assert behaviour. Teardown (cancel + `testEnv.Stop()`) happens after `m.Run()` returns.

**Why start the Manager in a goroutine?**
`mgr.Start(ctx)` blocks until the context is cancelled. Calling it in `TestMain` directly would block the test suite. Running it in a goroutine allows `m.Run()` to proceed while the manager runs in the background. `testCancel()` in the sequential teardown (after `m.Run()` returns) stops the manager.

**Why poll with a deadline instead of sleeping?**
The reconciler runs asynchronously — after you call `k8sClient.Create(ctx, widget)`, the watch event travels through the informer, is enqueued, and the reconciler runs on a separate goroutine. You cannot know exactly when it finishes. Sleeping a fixed duration is either too short (flaky) or too long (slow). Polling with a deadline (e.g., 10 seconds, checking every 100ms) is the standard pattern: fail fast when things work, and fail clearly when they do not.

**Why `KUBEBUILDER_ASSETS` and `setup-envtest`?**
`envtest` needs `etcd` and `kube-apiserver` binaries. They are not compiled into your Go binary. `setup-envtest` is a tool from `sigs.k8s.io/controller-runtime/tools/setup-envtest` that downloads the correct version of these binaries for your platform and prints their path. Setting `KUBEBUILDER_ASSETS` to that path tells `envtest.Environment.BinaryAssetsDirectory` where to find them.

**Why load CRDs from YAML files?**
The test cluster starts with no custom resources. `CRDInstallOptions.Paths` points to the CRD YAML file(s) to install. `envtest` applies them via the API server before any tests run. This is the same CRD that would be deployed in production, so tests exercise the real OpenAPI validation schema.

**Why `ctrl.SetControllerReference` in the reconciler?**
Owner references enable Kubernetes garbage collection: when the parent Widget is deleted, the API server automatically deletes the owned ConfigMap. Setting them is best practice for any child resource. The reference includes the parent's GVK and UID.

## How to run

**One-time setup:**
```bash
# Install setup-envtest
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

# Download binaries for Kubernetes 1.37
setup-envtest use 1.37

# Export the path (add this to your shell profile)
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
echo $KUBEBUILDER_ASSETS
# Example: /Users/you/Library/Application Support/io.kubebuilder.envtest/k8s/1.37.0-darwin-arm64
```

**Run the tests:**
```bash
cd 23-envtest-controller

export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
go test ./... -v
```

Expected output (abbreviated):
```
=== RUN   TestWidgetReconciler_CreatesConfigMap
--- PASS: TestWidgetReconciler_CreatesConfigMap (0.31s)
=== RUN   TestWidgetReconciler_SetsStatusReady
--- PASS: TestWidgetReconciler_SetsStatusReady (0.28s)
PASS
ok      envtest-controller      3.142s
```

The test suite takes ~3 seconds: ~2s for `etcd`/`kube-apiserver` startup, ~0.3s per test.

**Run with the race detector:**
```bash
go test -race ./... -v
```

**Verbose output showing manager logs:**
The manager runs with `zap.UseDevMode(true)`, so you will see reconciler log lines interleaved with test output when running with `-v`.

**If `KUBEBUILDER_ASSETS` is not set:**
```
panic: KUBEBUILDER_ASSETS must be set
  setup-envtest use 1.37 -p path
```

## What to observe

- The first ~2 seconds show no test output — this is `testEnv.Start()` launching `etcd` and `kube-apiserver` processes.
- After startup, the Manager logs appear: informers syncing, the controller starting.
- In `TestWidgetReconciler_CreatesConfigMap`: after `k8sClient.Create(ctx, widget)`, the test polls every 100ms. Within 200–400ms, the reconciler has run and created `envtest-widget-config`. Check the ConfigMap's `ownerReferences` — it points back to the Widget.
- In `TestWidgetReconciler_SetsStatusReady`: poll until `status.ready = true`. This exercises the status subresource — the reconciler calls `r.Status().Update()` which hits the real `/status` endpoint.
- After `m.Run()` returns, `testCancel()` stops the manager and `testEnv.Stop()` kills the `etcd`/`kube-apiserver` processes. Teardown runs sequentially after `m.Run()` — note it is *not* deferred, so if a panic occurs in `TestMain` setup before `m.Run()`, the processes are not cleaned up.
- Compare the test duration with case 22 (milliseconds): envtest is ~100x slower but gives ~100x more fidelity.

## Key code walkthrough

**`reconciler_test.go` lines 129–136 — envtest setup:**
```go
testEnv = &envtest.Environment{
    BinaryAssetsDirectory: binDir,
    CRDInstallOptions: envtest.CRDInstallOptions{
        Paths: []string{"widget_crd.yaml"},
    },
}
cfg, err := testEnv.Start()
```
`cfg` is a `*rest.Config` pointing at the freshly started API server. Everything downstream uses this config.

**`reconciler_test.go` lines 150–169 — Manager started in goroutine:**
```go
mgr, err := manager.New(cfg, manager.Options{...})
ctrl.NewControllerManagedBy(mgr).For(&Widget{}).Complete(&WidgetReconciler{...})

go func() {
    if err := mgr.Start(testCtx); err != nil {
        panic(err)
    }
}()
```
The Manager runs for the entire duration of the test suite. `testCtx` is cancelled in teardown.

**`reconciler_test.go` lines 192–203 — polling pattern:**
```go
deadline := time.Now().Add(10 * time.Second)
for time.Now().Before(deadline) {
    err := k8sClient.Get(testCtx,
        types.NamespacedName{Name: "envtest-widget-config", Namespace: "default"}, &cm)
    if err == nil {
        break
    }
    time.Sleep(100 * time.Millisecond)
}
if cm.Name == "" {
    t.Fatal("timed out waiting for ConfigMap to be created")
}
```
10-second deadline, 100ms poll interval. Fails clearly with a timeout message. Succeeds as soon as the condition is met — typically within 200–400ms.

**`reconciler_test.go` lines 97–102 — reconciler sets owner reference:**
```go
_ = ctrl.SetControllerReference(widget, cm, r.Scheme())
if err := r.Create(ctx, cm); client.IgnoreAlreadyExists(err) != nil {
    return reconcile.Result{}, err
}
widget.Status.Ready = true
return reconcile.Result{}, r.Status().Update(ctx, widget)
```
`SetControllerReference` wires the owner reference. `Status().Update()` uses the status subresource — validated by the real API server.

## References

- [controller-runtime envtest godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest)
- [upstream source — envtest](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/envtest/server.go)
- [setup-envtest godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest)
- [upstream source — setup-envtest](https://github.com/kubernetes-sigs/controller-runtime/tree/main/tools/setup-envtest)
- [Kubebuilder book — writing integration tests](https://book.kubebuilder.io/cronjob-tutorial/writing-tests.html)
- [Go TestMain documentation](https://pkg.go.dev/testing#hdr-Main)
