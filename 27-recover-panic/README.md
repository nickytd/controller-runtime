# Case 27 — Reconciler Panic Recovery (`RecoverPanic`)

## What this case covers

A panic inside `Reconcile` — a nil-pointer deref, an out-of-range index — would
by default crash the entire controller process, taking every other controller in
the Manager down with it. `controller.Options.RecoverPanic` turns that panic into
a recovered error and a requeue instead, so one bad object cannot wedge the whole
operator.

## Key concepts

| Concept | Description |
|---|---|
| `controller.Options.RecoverPanic` | `*bool` — recover panics in `Reconcile` and requeue, or let them crash the process |
| Default | `true` when unset (and when the Manager-level `Controller.RecoverPanic` is also unset) |
| On panic, recovered | Logged, surfaced as a reconcile error, item requeued with rate-limited backoff |
| On panic, not recovered | Propagates up and crashes the process |

## How to run

```bash
# Requires a reachable cluster in ~/.kube/config
go run ./...
```

In another terminal, create a ConfigMap named `boom` to trigger the panic:

```bash
kubectl create configmap boom --from-literal=k=v -n default
```

With recovery on (this case), you'll see the panic logged with a stack trace and
the request requeued — the process stays up. Edit `main.go` to set
`recoverPanic := false` and repeat to watch the process exit instead.

## Concepts in depth

### Why recover panics at all

A controller typically hosts many reconcilers in one process. Without recovery, a
panic triggered by a single malformed object crashes every controller at once —
an availability bug disguised as a code bug. Recovery contains the blast radius to
the offending item: it fails, backs off, and retries, while unrelated objects keep
reconciling.

### Why it still isn't a substitute for not panicking

Recovery keeps the process alive, but a reconciler that panics on `boom` will
panic on every retry — the item churns through the backoff queue forever and
emits `controller_runtime_reconcile_panics_total` metrics. Treat a recovered
panic as a loud signal to fix the reconciler, not as graceful error handling.
Expected failure conditions should return an `error` (or a `TerminalError`), not
panic.

### Interaction with the Manager default

If you leave `RecoverPanic` nil on the controller, it inherits the Manager's
`Controller.RecoverPanic`. If that is also nil, it defaults to `true`. This case
sets it explicitly so the behavior is self-documenting.

## References

- [controller.Options.RecoverPanic godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller#Options)
- [reconcile.TerminalError](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile#TerminalError)
- [companion: case 04 — result & requeue](../04-result-and-requeue)
