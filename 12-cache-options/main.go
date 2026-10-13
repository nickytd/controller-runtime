// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 12: Cache options
//
// Demonstrates:
//   - cache.Options.ByObject — per-type namespace and label selector scoping
//   - cache.ByObject with Namespaces map and LabelSelector
//   - DefaultTransform — strip managedFields from all cached objects
//   - Namespace-scoped cache: controller only sees objects in "default"
//   - cache.ErrCacheNotStarted — what happens if you read before the cache warms
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type CacheReconciler struct{ client.Client }

func (r *CacheReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	// managedFields will be empty — stripped by DefaultTransform
	logger.Info("reconciled", "name", cm.Name, "managedFields", len(cm.ManagedFields))
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
		Cache: cache.Options{
			// Scope ConfigMaps to "default" namespace with label selector
			ByObject: map[client.Object]cache.ByObject{
				&corev1.ConfigMap{}: {
					Namespaces: map[string]cache.Config{
						"default": {
							LabelSelector: labels.SelectorFromSet(labels.Set{"app": "cache-demo"}),
						},
					},
				},
			},
			// Strip managedFields from ALL cached objects to reduce RAM
			DefaultTransform: cache.TransformStripManagedFields(),
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(&CacheReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}

	// Reading through mgr.GetClient() before Start() returns
	// cache.ErrCacheNotStarted — the cache has not been populated yet, so all
	// reads must happen inside Reconcile (or after the cache has synced).

	setupLog.Info("cache scoped to default/app=cache-demo ConfigMaps only")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
