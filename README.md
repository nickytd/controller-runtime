# controller-runtime Learning Lab

[![CI](https://github.com/nickytd/controller-runtime/actions/workflows/ci.yml/badge.svg)](https://github.com/nickytd/controller-runtime/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.27-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

A hands-on workspace for deeply learning [`kubernetes-sigs/controller-runtime`](https://github.com/kubernetes-sigs/controller-runtime).
Each top-level folder is an independent Go module with a focused, runnable example that builds on the previous one.

> **Prerequisite**: Familiarity with `k8s.io/client-go` fundamentals (informers, watches, work queues).
> Work through the companion repo [`client-go` Learning Lab](https://github.com/nickytd/client-go) first.

---

## Quick start

```bash
cd controller-runtime/

# Run any case from its folder
cd 01-manager-basics
go run ./...

# Build every case (runs `go build ./...` inside each module)
make build

# Integration test (case 23) — needs envtest binaries
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
cd 23-envtest-controller && go test ./...
```

---

## Learning path

Work through the cases in order — each tier builds on the previous.

### Contents

- **Foundation** — [01 manager-basics](01-manager-basics/) · [02 first-reconciler](02-first-reconciler/) · [03 client-crud](03-client-crud/) · [04 result-and-requeue](04-result-and-requeue/) · [05 scheme-and-crd](05-scheme-and-crd/)
- **Controller Wiring** — [06 owns-and-owned](06-owns-and-owned/) · [07 predicates](07-predicates/) · [08 watches-and-handlers](08-watches-and-handlers/) · [09 external-events](09-external-events/) · [10 status-subresource](10-status-subresource/)
- **Production Patterns** — [11 finalizers](11-finalizers/) · [12 cache-options](12-cache-options/) · [13 field-indexer](13-field-indexer/) · [14 leader-election](14-leader-election/) · [15 server-side-apply](15-server-side-apply/) · [16 metrics](16-metrics/) · [17 logging](17-logging/)
- **Webhooks** — [18 webhook-validating](18-webhook-validating/) · [19 webhook-mutating](19-webhook-mutating/) · [20 webhook-conversion](20-webhook-conversion/) · [21 webhook-standalone](21-webhook-standalone/)
- **Testing** — [22 fake-client](22-fake-client/) · [23 envtest-controller](23-envtest-controller/)
- **Operational Concerns** — [24 health-checks](24-health-checks/) · [25 concurrency-and-ratelimit](25-concurrency-and-ratelimit/) · [26 priority-queue](26-priority-queue/) · [27 recover-panic](27-recover-panic/)

### Tier 1 — Foundation

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 01 | [`01-manager-basics`](01-manager-basics/) | `pkg/manager`, `pkg/log` | `manager.New()`, `Options`, signal-aware `Start(ctx)`, `ctrl.SetLogger` |
| 02 | [`02-first-reconciler`](02-first-reconciler/) | `pkg/reconcile`, `pkg/builder` | Minimal `Reconciler`, `.For().Complete()` wired to ConfigMaps, loop lifecycle |
| 03 | [`03-client-crud`](03-client-crud/) | `pkg/client` | `Get`/`List`/`Create`/`Update`/`Delete`, `MatchingLabels`, `IgnoreNotFound` |
| 04 | [`04-result-and-requeue`](04-result-and-requeue/) | `pkg/reconcile` | `Result{RequeueAfter}`, `TerminalError`, nil vs error backoff |
| 05 | [`05-scheme-and-crd`](05-scheme-and-crd/) | `runtime.Scheme`, `pkg/client` | Register hand-written CRD type, `AddToScheme`, CRUD via controller client |

### Tier 2 — Controller Wiring

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 06 | [`06-owns-and-owned`](06-owns-and-owned/) | `pkg/builder`, `pkg/client` | `SetControllerReference`, `.Owns()`, `CreateOrUpdate`, cascading reconcile |
| 07 | [`07-predicates`](07-predicates/) | `pkg/predicate`, `pkg/event` | `GenerationChangedPredicate`, `LabelChangedPredicate`, `And`/`Or`/`Not`, custom `TypedFuncs` |
| 08 | [`08-watches-and-handlers`](08-watches-and-handlers/) | `pkg/controller`, `pkg/source`, `pkg/handler` | `controller.New()`, `source.Kind()`, `EnqueueRequestForObject`, `EnqueueRequestForOwner`, `EnqueueRequestsFromMapFunc` |
| 09 | [`09-external-events`](09-external-events/) | `pkg/source` | `source.Channel()`, `GenericEvent` from a ticker, non-Kubernetes triggers |
| 10 | [`10-status-subresource`](10-status-subresource/) | `pkg/client` | `Status().Update()` vs `Update()`, why they differ, status CRD schema |

### Tier 3 — Production Patterns

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 11 | [`11-finalizers`](11-finalizers/) | `pkg/finalizer` | `NewFinalizers().Register()`, `Finalize()`, structured cleanup vs manual string checks |
| 12 | [`12-cache-options`](12-cache-options/) | `pkg/cache`, `pkg/manager` | `ByObject` namespace/label/field scoping, `TransformStripManagedFields` |
| 13 | [`13-field-indexer`](13-field-indexer/) | `pkg/client` (FieldIndexer) | `IndexField` at startup, `MatchingFields` for fast List queries |
| 14 | [`14-leader-election`](14-leader-election/) | `pkg/manager` | `LeaderElection` option, `LeaderElectionRunnable`, `Elected()` channel |
| 15 | [`15-server-side-apply`](15-server-side-apply/) | `pkg/client` | `Apply()` with `FieldOwner`, SSA vs merge patch, conflict resolution |
| 16 | [`16-metrics`](16-metrics/) | `pkg/metrics`, `pkg/manager` | Prometheus registry, custom counter/histogram in a reconciler, metrics endpoint |
| 17 | [`17-logging`](17-logging/) | `pkg/log`, `logr`, `log/slog` | `ctrl.SetLogger(logr.FromSlogHandler(...))`, `log.FromContext(ctx)`, structured key-value fields |

### Tier 4 — Webhooks

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 18 | [`18-webhook-validating`](18-webhook-validating/) | `pkg/webhook/admission`, `pkg/builder` | `Validator[T]`, `ValidateCreate/Update/Delete`, builder registration |
| 19 | [`19-webhook-mutating`](19-webhook-mutating/) | `pkg/webhook/admission` | `Defaulter[T]`, `Default()`, `Patched()` response, execution order |
| 20 | [`20-webhook-conversion`](20-webhook-conversion/) | `pkg/webhook/conversion` | Multi-version CRD, hub-spoke pattern, `conversion.Convertible` |
| 21 | [`21-webhook-standalone`](21-webhook-standalone/) | `pkg/webhook/admission` | `StandaloneWebhook`, webhook without a Manager, direct HTTP handler |

### Tier 5 — Testing

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 22 | [`22-fake-client`](22-fake-client/) | `pkg/client/fake` | `fake.NewClientBuilder().WithObjects().Build()`, `WithStatusSubresource`, unit-testing reconcilers |
| 23 | [`23-envtest-controller`](23-envtest-controller/) | `pkg/envtest`, `pkg/manager` | `_test.go`: real API server, `InstallCRDs`, full controller in envtest, integration assertions |

### Tier 6 — Operational Concerns

| # | Folder | Package(s) | What you learn |
|---|--------|------------|----------------|
| 24 | [`24-health-checks`](24-health-checks/) | `pkg/manager`, `pkg/healthz` | `HealthProbeBindAddress`, `AddHealthzCheck`/`AddReadyzCheck`, liveness vs readiness, cache-sync gating |
| 25 | [`25-concurrency-and-ratelimit`](25-concurrency-and-ratelimit/) | `pkg/controller`, `pkg/builder` | `MaxConcurrentReconciles`, custom `RateLimiter` via `.WithOptions`, per-object idempotency |
| 26 | [`26-priority-queue`](26-priority-queue/) | `pkg/controller`, `k8s.io/client-go/util/workqueue` | `controller.Options.NewQueue`, wrapping the default rate-limiting queue, reconcile ordering |
| 27 | [`27-recover-panic`](27-recover-panic/) | `pkg/controller` | `controller.Options.RecoverPanic`, recovered panic vs process crash, blast-radius containment |

---

## Repository layout

Each case is a fully self-contained Go module — there is no root `go.work`.
Build or run any case from inside its own folder. Every case folder also
contains its own `README.md` with a concept walkthrough, alongside `go.mod`,
`main.go` (or `reconciler_test.go` for case 23), and — for cases that define a
custom resource — a `widget_crd.yaml` manifest alongside it.

---

## Key dependencies

| Package | Used in |
|---------|---------|
| `sigs.k8s.io/controller-runtime` | All cases |
| `k8s.io/api` / `k8s.io/apimachinery` / `k8s.io/client-go` | All cases |
| `log/slog` (stdlib) via `logr.FromSlogHandler` | All cases (global logger) |
| `sigs.k8s.io/controller-runtime/pkg/log/zap` | Case 23 (test logger) |

---

## Possible future cases

Topics intentionally not yet covered — candidates for later additions:

- **Admission webhook certificate rotation** — managing serving certs for cases 18–21
- **Multi-cluster / `cluster.Cluster`** — reconciling across more than one API server

---

## References

- [controller-runtime godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [controller-runtime GitHub](https://github.com/kubernetes-sigs/controller-runtime)
- [Kubebuilder book](https://book.kubebuilder.io/)
- [Programming Kubernetes (O'Reilly)](https://www.oreilly.com/library/view/programming-kubernetes/9781492047094/)
- [API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
- [companion: client-go Learning Lab](https://github.com/nickytd/client-go)

---

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
