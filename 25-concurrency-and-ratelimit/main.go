// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 25: Concurrency & rate limiting
//
// Demonstrates:
//   - controller.Options.MaxConcurrentReconciles to run N reconciles in parallel
//   - Supplying a custom workqueue RateLimiter via controller.Options
//   - How this maps to the client-go workqueue rate-limiting case (controller-runtime
//     hides the queue and exposes it as builder options)
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

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

// ConfigMapReconciler logs each reconcile so concurrent runs are visible.
type ConfigMapReconciler struct{}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("reconciling", "name", req.Name, "namespace", req.Namespace)
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

	// A per-item exponential backoff rate limiter (10ms up to 60s). This is the
	// per-item half of controller-runtime's default MaxOfRateLimiter.
	rl := workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
		10*time.Millisecond, 60*time.Second,
	)

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 3,  // up to 3 reconciles run in parallel
			RateLimiter:             rl, // custom per-item backoff
		}).
		Complete(&ConfigMapReconciler{}); err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	setupLog.Info("watching ConfigMaps with MaxConcurrentReconciles=3 — create several to see parallel reconciles")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
