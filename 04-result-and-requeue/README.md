# Case 04: Result and Requeue

The `reconcile.Result` return value is your controller's way of telling the work queue how — and whether — to call you again. Getting the requeue strategy right is essential for building controllers that are both responsive and resource-efficient.

## What this case teaches

- `reconcile.Result{}` — success, no automatic requeue (only re-triggered by future events)
- `reconcile.Result{RequeueAfter: d}` — requeue after a fixed duration, regardless of events
- Returning a non-nil `error` — exponential backoff requeue with automatic retry
- `reconcile.TerminalError(err)` — permanent failure; the controller stops retrying this object
- How an in-memory counter (`atomic.Int64`) demonstrates each strategy in sequence on a single object

## Key concepts

### Why four different requeue strategies?

Different failure modes need different responses:

| Strategy | When to use |
|---|---|
| `Result{}` (no requeue) | Work is done; wait for the next event |
| `Result{RequeueAfter: d}` | Work requires polling — e.g., waiting for a Pod to become Ready |
| `return Result{}, err` | Transient failure — network error, API rate-limit, temporary unavailability |
| `TerminalError(err)` | Permanent failure — bad user input, CRD schema violation, resource that can never be created |

Using `return Result{}, err` for everything is the most common beginner mistake. It causes exponential-backoff storms when the failure is actually permanent (e.g., an invalid field), filling logs with useless retries and holding up the work queue.

### How does exponential backoff work?

When you return a non-nil error, the controller's work queue uses a rate limiter (by default the controller rate limiter from client-go's `workqueue`) to reschedule the item. The delay starts small (a few milliseconds), doubles on each consecutive failure, and is capped (a few hundred seconds); the exact constants are a client-go default and have shifted across versions, so treat the numbers as illustrative. The per-item delay resets when the item next reconciles without error. This is identical to how client-go work queues behave in raw controllers.

### What does TerminalError actually do internally?

`reconcile.TerminalError(err)` wraps the error in a sentinel type that the controller checks before re-enqueuing. If the error is terminal, the controller logs it and drops the item from the work queue entirely. The item is only re-enqueued if a new event arrives for the object (e.g., the user edits it to fix the bad input). The underlying error message is still logged and visible via `kubectl describe`.

### Why use `atomic.Int64` instead of a regular field?

The reconcile method may be called from multiple goroutines concurrently (the default concurrency is 1 per controller, but it is configurable). Using `atomic.Int64` makes the counter safe without a mutex. In real controllers you would store state in the object's `Status` subresource rather than in memory — in-memory counters reset on pod restart.

### What is the difference between `RequeueAfter` and returning an error?

`RequeueAfter` schedules a deterministic future reconcile. It is appropriate when you know roughly how long to wait (e.g., "check again in 30 seconds whether the external database is ready"). Returning an error uses backoff, so the timing is unpredictable from the controller's perspective — it is appropriate when you do not know how long the failure will last.

### Why filter to a single ConfigMap?

`For(&corev1.ConfigMap{})` watches **every** ConfigMap in the cluster, so the reconciler would also fire for `kube-root-ca.crt`, leader-election leases, and any other ConfigMap — each one bumping the shared `attempts` counter and scrambling the demo sequence. A name predicate on the builder scopes the controller to the single `requeue-demo` object:

```go
For(&corev1.ConfigMap{}, builder.WithPredicates(
    predicate.NewPredicateFuncs(func(obj client.Object) bool {
        return obj.GetName() == demoName
    }),
))
```

The predicate runs before the object is enqueued, so filtered-out ConfigMaps never reach `Reconcile` and never touch the counter. This keeps the logs legible and the attempt numbering deterministic.

## How to run

```bash
cd 04-result-and-requeue
go run ./...
```

In another terminal, create one ConfigMap to trigger the demo sequence:

```bash
kubectl create configmap requeue-demo -n default
```

## What to observe

Watch the controller logs after creating the ConfigMap. You will see the reconciler cycle through each strategy:

```
# Attempt 1 — returns error, enters backoff
time=2024-01-15T10:23:01.000+01:00 level=INFO msg="attempt failed, will backoff" attempt=1
time=2024-01-15T10:23:01.000+01:00 level=ERROR msg="Reconciler error" error="transient error — will backoff and retry"

# Attempt 2 — same error, backoff delay increases
time=2024-01-15T10:23:01.005+01:00 level=INFO msg="attempt failed, will backoff" attempt=2
time=2024-01-15T10:23:01.005+01:00 level=ERROR msg="Reconciler error" error="transient error — will backoff and retry"

# Attempt 3 — RequeueAfter: 10s (no error line, just a scheduled requeue)
time=2024-01-15T10:23:01.015+01:00 level=INFO msg="requeueing after delay" attempt=3

# ~10 seconds later, attempt 4 — TerminalError, no further retries
time=2024-01-15T10:23:11.015+01:00 level=INFO msg="terminal failure, giving up" attempt=4
time=2024-01-15T10:23:11.015+01:00 level=ERROR msg="Reconciler error" error=unrecoverable

# If you edit the ConfigMap to trigger a new event, attempt 5+ succeed
time=2024-01-15T10:24:30.000+01:00 level=INFO msg="reconciled successfully" attempt=5
```

Key things to notice:
- After attempt 4 (TerminalError), no further reconciles happen unless a new event arrives. The "giving up" is conveyed by the error *type* (`reconcile.TerminalError`), not by a field on the error log line — the controller simply stops re-enqueuing the item.
- The delay between attempts 1 and 2 grows visibly (check timestamps).
- Attempt 3 does NOT produce an error log line — `RequeueAfter` is a deliberate requeue, not a failure.

To trigger attempt 5 after the TerminalError, edit the ConfigMap:

```bash
kubectl annotate configmap requeue-demo restart=now -n default
```

## Key code walkthrough

**The four return paths** (`main.go:47-67`):
```go
case attempt <= 2:
    return reconcile.Result{}, errTransient          // backoff retry

case attempt == 3:
    return reconcile.Result{RequeueAfter: 10 * time.Second}, nil  // scheduled retry

case attempt == 4:
    return reconcile.Result{}, reconcile.TerminalError(errors.New("unrecoverable"))  // no retry

default:
    return reconcile.Result{}, nil                   // success
```

Each arm demonstrates a distinct error-handling philosophy. The switch is sequential because `atomic.Int64.Add` returns the new value atomically.

**atomic counter** (`main.go:36-39`):
```go
type RequeueReconciler struct {
    client.Client
    attempts atomic.Int64
}
```
`attempts.Add(1)` atomically increments and returns the new value in a single operation, making it safe under concurrent access without a mutex.

## References

- [reconcile.Result godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile#Result)
- [reconcile.TerminalError godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile#TerminalError)
- [upstream source: pkg/reconcile/reconcile.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/reconcile/reconcile.go)
- [upstream source: pkg/internal/controller/controller.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/internal/controller/controller.go)
- [client-go workqueue rate limiter](https://pkg.go.dev/k8s.io/client-go/util/workqueue#DefaultControllerRateLimiter)
