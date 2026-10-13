# Case 16: Custom Metrics

Instrument a reconciler with Prometheus metrics — counter, gauge, and histogram — and expose them via controller-runtime's built-in `/metrics` endpoint.

## What this case teaches

- `metrics.Registry.MustRegister(...)` — register with controller-runtime's shared Prometheus registry (not `prometheus.DefaultRegisterer`)
- `prometheus.NewCounterVec` with labels — count reconciles broken down by outcome
- `prometheus.NewCounter` — count errors without labels
- `prometheus.NewGauge` — track a current value that goes up and down (active ConfigMaps)
- `prometheus.NewHistogram` with `prometheus.DefBuckets` — measure reconcile duration
- `prometheus.NewTimer(histogram).ObserveDuration()` with `defer` — time the entire reconcile function
- `metricsserver.Options{BindAddress: ":8080"}` — expose `/metrics` on a real port
- `curl http://localhost:8080/metrics | grep widget_` — scrape and inspect your metrics

## Key concepts

### Why metrics.Registry, not prometheus.DefaultRegisterer?

`prometheus.DefaultRegisterer` is the global Prometheus registry used by the standard Go client and many libraries. `metrics.Registry` is controller-runtime's own Prometheus registry, and this is where the `/metrics` endpoint served by the manager reads from.

controller-runtime already registers its own internal metrics (reconcile queue depth, work queue latency, etc.) into `metrics.Registry`. By registering your custom metrics into the same registry, they appear alongside the built-in ones at the same `/metrics` endpoint. Using `prometheus.DefaultRegisterer` would register your metrics in a different registry that is not served by the manager's endpoint.

The practical consequence: if you register with `prometheus.DefaultRegisterer` and scrape `localhost:8080/metrics`, your custom metrics will be absent. Register with `metrics.Registry` and they appear.

### The `init()` registration pattern

```go
func init() {
    metrics.Registry.MustRegister(reconcileTotal, reconcileErrors, activeCMs, reconcileDuration)
}
```

Registering in `init()` ensures metrics are registered before `main()` runs. `MustRegister` panics on duplicate registration — this is intentional. If the same metric is accidentally registered twice (e.g., in two `init()` functions that both run), a panic at startup is better than silent metric collision at scrape time.

### Choosing the right metric type

| Type | When to use | Example |
|---|---|---|
| Counter | Things that only go up; count events | Reconcile calls, errors |
| CounterVec | Counter with variable labels | Reconciles by outcome (`success`/`not_found`/`error`) |
| Gauge | A value that goes up and down | Number of managed resources |
| Histogram | Distribution of values; latency | Reconcile duration |

A `Histogram` is the right type for latency. It gives you percentiles (p50, p95, p99) via `histogram_quantile` in PromQL, unlike a simple average from a Counter/Gauge pair. `prometheus.DefBuckets` provides a reasonable set of latency buckets from 5ms to 10s — adjust these if your reconcile loop is expected to run in microseconds or minutes.

### Timing with defer

```go
func (r *MetricsReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
    timer := prometheus.NewTimer(reconcileDuration)
    defer timer.ObserveDuration()
    // ... all reconcile logic ...
}
```

`prometheus.NewTimer` starts a wall-clock timer. `ObserveDuration()` records the elapsed time into the histogram. Using `defer` ensures the observation happens regardless of how the function returns — success, error, or early return. Placing it on the second line of the function (after the timer is created) captures the full reconcile duration including all subsequent calls.

### Labels on counters

```go
reconcileTotal.WithLabelValues("success").Inc()
reconcileTotal.WithLabelValues("not_found").Inc()
reconcileTotal.WithLabelValues("error").Inc()
reconcileErrors.Inc() // the plain Counter, incremented only on real errors
```

`CounterVec` allows breaking down a single counter by label values. This avoids defining two separate counters (`reconcileSuccessTotal` and `reconcileNotFoundTotal`) and enables PromQL aggregation:

```promql
# Total reconcile rate
sum(rate(widget_reconcile_total[5m]))

# Success ratio
rate(widget_reconcile_total{result="success"}[5m]) /
rate(widget_reconcile_total[5m])
```

### The metrics bind address

```go
Metrics: metricsserver.Options{BindAddress: ":8080"},
```

Other cases in this series use `BindAddress: "0"` to disable the metrics server (avoiding port conflicts). This case uses `:8080` to expose a real endpoint. In production, this should be behind authentication (see `metricsserver.Options.FilterProvider` for Kubernetes RBAC-based scraping).

## How to run

```bash
cd 16-metrics
go run ./...
```

The controller prints:

```
metrics available at http://localhost:8080/metrics
```

In a separate terminal, create ConfigMaps to generate metric data:

```bash
# Create several ConfigMaps in the default namespace
for i in 1 2 3; do
  kubectl create configmap demo-$i --namespace default --from-literal=key=val$i
done
```

Then scrape the metrics endpoint:

```bash
curl -s http://localhost:8080/metrics | grep widget_
```

## What to observe

After a few reconciles, the output of `curl http://localhost:8080/metrics | grep widget_` will show:

```
# HELP widget_active_configmaps Number of ConfigMaps currently tracked.
# TYPE widget_active_configmaps gauge
widget_active_configmaps 42

# HELP widget_reconcile_duration_seconds Reconcile call duration.
# TYPE widget_reconcile_duration_seconds histogram
widget_reconcile_duration_seconds_bucket{le="0.005"} 12
widget_reconcile_duration_seconds_bucket{le="0.01"} 12
...
widget_reconcile_duration_seconds_count 12
widget_reconcile_duration_seconds_sum 0.002341

# HELP widget_reconcile_errors_total Total number of reconcile errors.
# TYPE widget_reconcile_errors_total counter
widget_reconcile_errors_total 0

# HELP widget_reconcile_total Total number of reconcile calls.
# TYPE widget_reconcile_total counter
widget_reconcile_total{result="not_found"} 0
widget_reconcile_total{result="success"} 12
```

Notice:
- `widget_active_configmaps` reflects the number of ConfigMaps in the namespace — it is a gauge (not always incrementing)
- `widget_reconcile_total` is split by `result` label
- The histogram's `_bucket` lines accumulate across all reconciles (they are cumulative, not per-call)

Also observe the built-in controller-runtime metrics that appear in the same scrape:

```bash
curl -s http://localhost:8080/metrics | grep controller_runtime_
```

These include `controller_runtime_reconcile_total`, `controller_runtime_reconcile_errors_total`, and work queue metrics — already instrumented for you.

## Key code walkthrough

**Package-level metric variables** (`main.go:28-49`):
```go
var (
    reconcileTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "widget_reconcile_total",
        Help: "Total number of reconcile calls.",
    }, []string{"result"})

    reconcileDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name:    "widget_reconcile_duration_seconds",
        Help:    "Reconcile call duration.",
        Buckets: prometheus.DefBuckets,
    })
)
```
Package-level variables ensure the metric instances are singletons. The `Name` follows Prometheus naming conventions: `<namespace>_<subsystem>_<name>_<unit>`.

**Registration in init** (`main.go:51-53`):
```go
func init() {
    metrics.Registry.MustRegister(reconcileTotal, reconcileErrors, activeCMs, reconcileDuration)
}
```
`metrics.Registry` (not `prometheus.DefaultRegisterer`) is essential.

**Timer with defer** (`main.go:59-60`):
```go
timer := prometheus.NewTimer(reconcileDuration)
defer timer.ObserveDuration()
```
Two lines at the top of `Reconcile` — the minimal boilerplate for full-function duration measurement.

**Gauge set from list count** (`main.go:75-78`):
```go
cmList := &corev1.ConfigMapList{}
if err := r.List(ctx, cmList, client.InNamespace(req.Namespace)); err == nil {
    activeCMs.Set(float64(len(cmList.Items)))
}
```
A gauge is set to an absolute value, not incremented. Each reconcile recomputes the count and sets it. Note: `float64` cast is required — Prometheus gauges are always floating point.

**Metrics bind address** (`main.go:97`):
```go
Metrics: metricsserver.Options{BindAddress: ":8080"},
```
Contrast with other cases that use `"0"` to disable the server. `:8080` is the conventional port for Kubernetes operator metrics.

## References

- [controller-runtime metrics godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/metrics)
- [metricsserver godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/metrics/server)
- [upstream metrics/registry.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/metrics/registry.go)
- [Prometheus Go client godoc](https://pkg.go.dev/github.com/prometheus/client_golang/prometheus)
- [Prometheus metric naming best practices](https://prometheus.io/docs/practices/naming/)
