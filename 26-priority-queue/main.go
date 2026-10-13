// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 26: Custom work queue via controller.Options.NewQueue
//
// Demonstrates:
//   - Replacing the controller's work queue with controller.Options.NewQueue
//   - The NewQueue contract: a factory that receives the controller name and the
//     (defaulted) RateLimiter and returns a workqueue.TypedRateLimitingInterface
//   - Wrapping the default queue to observe/prioritize what gets reconciled first
//
// NewQueue is a low-level primitive. The standard queue is almost always correct;
// a custom queue is only needed when you must control reconcile ordering (e.g. a
// priority queue that reconciles changed objects before periodic resyncs).
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// loggingQueue wraps a TypedRateLimitingInterface and logs every add. A real
// priority queue would reorder items here (e.g. push high-priority Requests to
// the front); this example keeps default ordering and only observes it so the
// NewQueue wiring stays the focus.
type loggingQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	name string
}

// Add logs the item before delegating to the wrapped queue.
func (q *loggingQueue) Add(item reconcile.Request) {
	ctrl.Log.WithName("queue").Info("enqueue", "queue", q.name, "request", item.String())
	q.TypedRateLimitingInterface.Add(item)
}

// Reconciler is a no-op reconciler; the point of this case is the queue, not the work.
type Reconciler struct{}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("reconciling", "name", req.Name, "namespace", req.Namespace)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	setupLog := ctrl.Log.WithName("setup")

	cfg, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}

	mgr, err := manager.New(cfg, manager.Options{
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// NewQueue is called once, lazily, when the controller is about to start.
	// It receives the controller name and the resolved RateLimiter, and must
	// return the queue the controller will drive. Here we build the standard
	// rate-limiting queue and wrap it so every enqueue is observable.
	newQueue := func(name string, rl workqueue.TypedRateLimiter[reconcile.Request]) workqueue.TypedRateLimitingInterface[reconcile.Request] {
		base := workqueue.NewTypedRateLimitingQueueWithConfig(rl, workqueue.TypedRateLimitingQueueConfig[reconcile.Request]{
			Name: name,
		})
		return &loggingQueue{TypedRateLimitingInterface: base, name: name}
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithOptions(controller.Options{
			NewQueue: newQueue, // swap in the custom queue factory
		}).
		Complete(&Reconciler{}); err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	setupLog.Info("watching ConfigMaps with a custom NewQueue — create ConfigMaps to see enqueue logs")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
