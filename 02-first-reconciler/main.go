// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 02: First reconciler
//
// Demonstrates:
//   - Implementing the reconcile.Reconciler interface
//   - Wiring a reconciler to ConfigMaps via ctrl.Builder (.For().Complete())
//   - The reconcile Request carries only name+namespace, not the event
//   - Logging with ctrl.LoggerFrom(ctx)
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ConfigMapReconciler reconciles ConfigMap objects.
type ConfigMapReconciler struct{}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("reconciling ConfigMap", "name", req.Name, "namespace", req.Namespace)
	// Real logic goes here: fetch the object, compare desired vs actual, act.
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

	// Wire reconciler to ConfigMaps — any create/update/delete triggers Reconcile.
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(&ConfigMapReconciler{}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}

	setupLog.Info("watching ConfigMaps — create/edit/delete one to see reconcile calls")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
