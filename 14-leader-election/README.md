# Case 14: Leader Election

Run multiple controller replicas safely by having only the elected leader execute controllers, while all replicas participate in health probing and readiness checks.

## What this case teaches

- `manager.Options.LeaderElection: true` — enable leader election for the manager
- `LeaderElectionID` and `LeaderElectionNamespace` — the identity of the Lease object used for coordination
- `LeaderElectionResourceLock: "leases"` — use Lease objects (the recommended lock type)
- `LeaseDuration`, `RenewDeadline`, `RetryPeriod` — the timing parameters that govern failover speed
- `NeedLeaderElection() bool` — the interface that controls whether a `Runnable` runs on all replicas or only the leader
- How `mgr.Start()` blocks until leadership is acquired before starting controllers
- The difference between `leaderWork` (leader-only) and `healthProbe` (all replicas)

## Key concepts

### Why leader election?

Kubernetes controllers are level-triggered: they reconcile the full desired state on every event, and reconciliation is idempotent. In theory, running two copies of the same controller simultaneously should be safe. In practice, it is not:

- Two reconcilers might both see a resource needing update, both read it, both compute a patch, and both try to write — causing conflicts and spurious updates.
- Finalizer cleanup might run twice, causing errors in external systems (double-delete of a database, double-revocation of a certificate).
- Some controllers perform admission-like mutations where running the logic twice produces incorrect results.

Leader election ensures only one replica is actively reconciling at any time. The others are hot standbys: they are running, holding a cache, and competing for the lock. If the leader crashes or loses connectivity, a standby wins the lease and takes over within seconds.

### Lease objects

The lock is a `coordination.k8s.io/v1 Lease` object stored in Kubernetes. The leader continuously renews it (every `RenewDeadline`, default 10s). A standby watches the Lease; if the `renewTime` has not been updated for `LeaseDuration` (default 15s), it tries to acquire the lock. `RetryPeriod` (default 2s) controls how often a standby re-attempts acquisition.

The `leases` lock type is preferred over the older `configmaps` and `endpoints` lock types. It creates fewer events, uses a dedicated API group, and is supported by all modern Kubernetes versions.

The Lease object is created at `default/ctrl-runtime-lab-leader` (namespace/name from the options):

```bash
kubectl get lease ctrl-runtime-lab-leader -n default -o yaml
```

### NeedLeaderElection()

Any type implementing `manager.Runnable` (`Start(ctx context.Context) error`) can also implement `NeedLeaderElection() bool`. This controls when the runnable is started:

- `NeedLeaderElection() = true` (or not implemented — defaults to true for controllers): the runnable only starts after this instance wins the election. If leadership is lost, `ctx` is cancelled and the runnable should stop.
- `NeedLeaderElection() = false`: the runnable starts immediately on all replicas, regardless of election status. Use this for health probes, metrics servers, and other work that all replicas should do.

Controllers registered via `ctrl.NewControllerManagedBy(mgr).For(...)` always have `NeedLeaderElection() = true`. You cannot make a controller run on non-leaders.

### mgr.Start() and leadership acquisition

`mgr.Start(ctx)` blocks. The sequence is:

1. Start all runnables with `NeedLeaderElection() = false` immediately (health probe, metrics server)
2. Compete for the Lease lock
3. Once elected, start all runnables with `NeedLeaderElection() = true` (controllers, `leaderWork`)
4. Block until the context is cancelled or the process exits

If the Lease is already held by another instance, step 2 blocks until the lock is acquired — there is no timeout. The replica is running and healthy (health probe is active) but no controllers are reconciling.

### Timing parameters

| Parameter | Default | Meaning |
|---|---|---|
| `LeaseDuration` | 15s | How long before a lock is considered expired |
| `RenewDeadline` | 10s | How long the leader has to renew before giving up |
| `RetryPeriod` | 2s | How often standbys try to acquire the lock |

Failover time is at most `LeaseDuration` (15s by default). In a well-connected cluster with quick pod restarts, it is typically 15-20 seconds from leader crash to standby taking over. Tightening these values reduces failover time but increases API server load from lease renewals.

## How to run

```bash
cd 14-leader-election

# Start the first replica — it will win the election
go run ./... &

# Start a second replica in another terminal — it will stand by
go run ./... &
```

To observe failover:

```bash
# Note which process is the leader (look for "LeaderWork: I am the leader")
# Kill that process with Ctrl-C or kill <pid>
# The standby will log "LeaderWork: I am the leader" within ~15 seconds
```

## What to observe

**On the leader**:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="HealthProbe: running on all replicas"
time=2024-01-15T10:23:01.300+01:00 level=INFO msg="LeaderWork: I am the leader, doing exclusive work"
time=2024-01-15T10:23:01.350+01:00 level=INFO msg="reconciling (leader only)" key=default/some-configmap
```

**On a standby**:

```
time=2024-01-15T10:23:01.234+01:00 level=INFO msg="HealthProbe: running on all replicas"
# (no LeaderWork log, no reconcile logs — standing by for the lease)
```

**After killing the leader**, the standby logs:

```
time=2024-01-15T10:23:46.000+01:00 level=INFO msg="LeaderWork: I am the leader, doing exclusive work"
time=2024-01-15T10:23:46.050+01:00 level=INFO msg="reconciling (leader only)" key=default/some-configmap
```

Inspect the Lease object to see who currently holds it:

```bash
kubectl get lease ctrl-runtime-lab-leader -n default \
  -o jsonpath='{.spec.holderIdentity}{"\n"}'
```

The `holderIdentity` is typically `<pod-name>_<uid>` when running in a cluster, or a hostname-based identifier when running locally.

## Key code walkthrough

**Leader election options** (`main.go:75-81`):
```go
LeaderElection:             true,
LeaderElectionID:           "ctrl-runtime-lab-leader",
LeaderElectionNamespace:    "default",
LeaderElectionResourceLock: "leases",
LeaseDuration:              nil, // 15s
RenewDeadline:              nil, // 10s
RetryPeriod:                nil, // 2s
```
`nil` durations use the built-in defaults. In production, set these explicitly to make the failover behaviour visible in code.

**Leader-only runnable** (`main.go:28-35`):
```go
func (l *leaderWork) NeedLeaderElection() bool { return true }

func (l *leaderWork) Start(ctx context.Context) error {
    ctrl.Log.Info("LeaderWork: I am the leader, doing exclusive work")
    <-ctx.Done()
    ctrl.Log.Info("LeaderWork: lost leadership or shutting down")
    return nil
}
```
`<-ctx.Done()` blocks until leadership is lost or the manager shuts down. The log on exit is important: it confirms the context was cancelled, meaning cleanup is running.

**All-replica runnable** (`main.go:37-46`):
```go
func (h *healthProbe) NeedLeaderElection() bool { return false }
```
This starts on every replica before the election is decided. Use this for Kubernetes readiness/liveness endpoints so the pod is marked Ready even before it wins the election.

**Registering runnables** (`main.go:88-89`):
```go
_ = mgr.Add(&leaderWork{})
_ = mgr.Add(&healthProbe{})
```
`mgr.Add` queues the runnable for the manager to start at the appropriate time.

## References

- [controller-runtime manager godoc](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager)
- [LeaderElectionRunnable interface](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager#LeaderElectionRunnable)
- [upstream manager/manager.go](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/manager/manager.go)
- [Kubernetes leader election design](https://github.com/kubernetes/client-go/tree/master/tools/leaderelection)
- [coordination.k8s.io Lease API](https://kubernetes.io/docs/reference/kubernetes-api/cluster-resources/lease-v1/)
