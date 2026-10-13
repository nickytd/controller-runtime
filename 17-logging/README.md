# Case 17: Structured Logging with logr and slog

Demonstrates how controller-runtime integrates structured logging through the `logr` abstraction, backed by the standard library's `log/slog`, including context-propagation of enriched loggers across call boundaries.

## What this case teaches

- How to bridge `log/slog` into controller-runtime with `logr.FromSlogHandler` and install it as the global logger
- How `slog.NewTextHandler` produces human-readable `key=value` output (and how to swap it for `slog.NewJSONHandler`)
- How `ctrl.LoggerFrom(ctx)` retrieves a logger pre-populated with reconcile context fields
- How `logger.WithValues()` attaches persistent key-value pairs to every subsequent log line
- How `logger.V(1).Info()` gates verbose output behind a log-level threshold, and how V-levels map onto slog levels
- How `ctrl.LoggerInto(ctx, logger)` stores an enriched logger back into the context so helpers don't need an explicit logger parameter

## Key concepts

**Why logr instead of a direct slog dependency?**
controller-runtime uses the `logr` interface throughout, not any concrete logger. This decouples the framework from any particular logging implementation. go-logr ships an adapter (`logr.FromSlogHandler`) that routes every log line into a `slog.Handler`, so the whole stack — manager, controllers, your reconciler — emits through slog with no third-party dependency. You could swap the slog handler for zap, zerolog, or a test logger without changing any framework code.

**Why inject the logger into the context?**
Deep call stacks in a reconciler can have many helpers. Threading a logger through every function signature is noisy and couples every helper to the logger type. Storing the logger in `context.Context` with `ctrl.LoggerInto` means helpers call `ctrl.LoggerFrom(ctx)` and get the same logger — already enriched with controller name, namespace, and any `WithValues` fields added higher up.

**Why `WithValues` instead of adding fields per log call?**
Fields added with `WithValues` are attached permanently to that logger instance. Any log line emitted through it, including lines from helper functions that receive the same context, will carry those fields. This ensures correlated log entries share a consistent set of fields without repetition.

**Why `logger.V(1).Info()` for verbose output?**
Production logs should be quiet by default. Verbose diagnostics (resource versions, cache internals, detailed diffs) are emitted at level 1 or higher so they are invisible in production but can be turned on with a flag without recompiling. The framework itself uses V(4)–V(5) for its own internal traces.

**How do logr V-levels map onto slog levels?**
logr verbosity `V(n)` becomes slog level `-n`. The default `Info` (`V(0)`) is `slog.LevelInfo` (0); `V(1)` is level -1, `V(2)` is level -2, and so on. To surface verbose logs you lower the handler's threshold below zero — this case does that with the `-verbose` flag, which sets the level to -1.

## How to run

This case watches ConfigMaps and requires a reachable cluster (a local kind cluster works).

```bash
cd 17-logging

# Default: info-level, console (text) handler
go run ./...

# Enable verbose (V(1)) logs by lowering the slog threshold to -1
go run ./... -verbose
```

To emit structured JSON instead of `key=value` text, swap `slog.NewTextHandler`
for `slog.NewJSONHandler` in `main.go` — the handler is the only thing that
changes; the logr bridge and all reconciler code stay the same.

Trigger reconcile events by creating or modifying ConfigMaps:

```bash
kubectl create configmap test-cm --from-literal=key=value
kubectl label configmap test-cm env=dev
kubectl delete configmap test-cm
```

## What to observe

**At default log level (`info`):**
```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="reconciling ConfigMap" controller=configmap name=test-cm namespace=default generation=unknown dataKeys=1 labels=map[env:dev]
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="doing work" controller=configmap name=test-cm namespace=default generation=unknown configmap=test-cm
```

Notice that:
- Both log lines carry `generation=unknown` — this came from `logger.WithValues()` on the parent logger, and was propagated through context to `doWork`.
- The `controller`, `name`, and `namespace` fields are injected automatically by the framework before your reconciler even runs.
- The `resourceVersion` verbose line does NOT appear.

**With `-verbose`:**
```
time=2024-01-15T10:23:01.235+01:00 level=DEBUG+3 msg="verbose detail" controller=configmap name=test-cm namespace=default generation=unknown resourceVersion=12345
```

The `V(1).Info()` line now appears. logr maps `V(1)` to slog level -1, and the text handler renders that relative to `LevelDebug` (-4) as `DEBUG+3`.

## Key code walkthrough

**`main.go` lines 73–86 — handler construction and logger installation:**
```go
verbose := flag.Bool("verbose", false, "lower the slog level to -1 so V(1) logs are emitted")
flag.Parse()

level := slog.LevelInfo
if *verbose {
    level = slog.Level(-1)
}

handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
ctrl.SetLogger(logr.FromSlogHandler(handler))
```
`logr.FromSlogHandler` wraps a `slog.Handler` as a `logr.Logger`, which is what
`ctrl.SetLogger` expects. Lowering the handler's `Level` below zero is what makes
`V(n)` lines visible.

**`main.go` lines 42–63 — context-propagated logger in reconcile:**
```go
logger := ctrl.LoggerFrom(ctx)                      // pre-populated by framework
logger = logger.WithValues("generation", "unknown") // attach extra field
// ...
ctx = ctrl.LoggerInto(ctx, logger)                  // store enriched logger
r.doWork(ctx, cm)                                   // helper gets same logger via ctx
```

**`main.go` lines 68–71 — helper uses context logger:**
```go
func (r *LoggingReconciler) doWork(ctx context.Context, cm *corev1.ConfigMap) {
    ctrl.LoggerFrom(ctx).Info("doing work", "configmap", cm.Name)
}
```
No `logger` parameter. The enriched logger (with `generation` attached) is retrieved from the context.

**`main.go` line 59 — verbose log:**
```go
logger.V(1).Info("verbose detail", "resourceVersion", cm.ResourceVersion)
```
Only emitted when the handler threshold is -1 or lower.

## References

- [controller-runtime log godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log)
- [upstream source — log](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/log/log.go)
- [go-logr/logr godoc](https://pkg.go.dev/github.com/go-logr/logr)
- [logr.FromSlogHandler godoc](https://pkg.go.dev/github.com/go-logr/logr#FromSlogHandler)
- [log/slog godoc](https://pkg.go.dev/log/slog)
