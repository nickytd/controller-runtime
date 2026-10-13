// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 03: Client CRUD
//
// Demonstrates:
//   - mgr.GetClient() — the cache-backed unified client
//   - Get, List with MatchingLabels and InNamespace
//   - Create, Update, Delete with error helpers
//   - client.IgnoreNotFound and client.IgnoreAlreadyExists
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type CRUDReconciler struct {
	client.Client
}

func (r *CRUDReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Get a single object (cache read)
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	logger.Info("got ConfigMap", "data keys", len(cm.Data))

	// List with label selector (cache read)
	cmList := &corev1.ConfigMapList{}
	if err := r.List(ctx, cmList,
		client.InNamespace(req.Namespace),
		client.MatchingLabels{"app": "crud-demo"},
	); err != nil {
		return reconcile.Result{}, err
	}
	logger.Info("listed ConfigMaps with label", "count", len(cmList.Items))

	// Create (direct API write — not cached)
	desired := &corev1.ConfigMap{
		Name:      "crud-child",
		Namespace: req.Namespace,
		Labels:    map[string]string{"app": "crud-demo"},
		Data:      map[string]string{"created-by": "crud-reconciler"},
	}
	if err := r.Create(ctx, desired); client.IgnoreAlreadyExists(err) != nil {
		return reconcile.Result{}, err
	}

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
	mgr, err := manager.New(cfg, manager.Options{Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(&CRUDReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up CRUD controller")
		os.Exit(1)
	}
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
