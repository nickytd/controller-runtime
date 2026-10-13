# Case 24 — Health & Readiness Probes

## What this case covers

A controller running in Kubernetes should expose liveness and readiness
endpoints so the kubelet can restart it when wedged and withhold traffic
until it is caught up. The Manager serves both on `HealthProbeBindAddress`.

## Key concepts

| Concept | Description |
|---|---|
| `HealthProbeBindAddress` | TCP address for the health server (`:8081` here; `"0"`/`""` disables it) |
| `mgr.AddHealthzCheck(name, check)` | Registers a liveness check served under `/healthz` |
| `mgr.AddReadyzCheck(name, check)` | Registers a readiness check served under `/readyz` |
| `healthz.Ping` | Built-in `healthz.Checker` that always succeeds — proves the process is alive |
| `healthz.Checker` | `func(*http.Request) error` — non-nil error means unhealthy |

## How to run

```bash
# Requires a reachable cluster in ~/.kube/config
go run ./...
```

In another terminal:

```bash
curl -i localhost:8081/healthz   # 200 ok once alive
curl -i localhost:8081/readyz    # 200 ok once the cache has synced
```

## Concepts in depth

### Liveness vs readiness

Liveness answers "is the process wedged and in need of a restart?" — keep it
cheap and dependency-free (`healthz.Ping`). Readiness answers "should I receive
work/traffic right now?" — gate it on real preconditions like cache sync, so a
freshly started replica does not act on a half-populated cache.

### Why gate readiness on cache sync

A controller's client reads from an informer cache. Before the cache has
synced, `List`/`Get` can return stale or empty results. `WaitForCacheSync`
blocks until the cache is ready (or the context is cancelled), so `/readyz`
stays pending — and the kubelet keeps the Pod out of rotation — until the
controller has a complete view. This matters during rollout when multiple
replicas start at once.

## References

- [healthz godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/healthz)
- [Manager.AddHealthzCheck / AddReadyzCheck](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager#Manager)
- [Kubernetes liveness/readiness probes](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/)
