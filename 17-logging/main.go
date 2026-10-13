// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 17: Structured logging with logr backed by slog
//
// Demonstrates:
//   - ctrl.SetLogger(logr.FromSlogHandler(...)) — bridge slog into controller-runtime
//   - slog.NewTextHandler — the human-readable console handler from the stdlib
//   - ctrl.LoggerFrom(ctx) — logger pre-populated with reconcile context
//   - logger.WithValues() — attach persistent key-value pairs
//   - logger.V(1).Info() — verbose levels, and how they map onto slog levels
//   - ctrl.LoggerInto / ctrl.LoggerFrom for passing loggers across call boundaries
//
// controller-runtime logs through logr.Logger. go-logr ships an adapter
// (logr.FromSlogHandler) that routes every log line into a slog.Handler, so the
// whole stack — manager, controllers, your reconciler — emits through slog with
// no extra dependency.
//
// V-level mapping: logr verbosity V(n) becomes slog level -n. The default
// Info (V(0)) is slog.LevelInfo (0); V(1) is level -1, V(2) is level -2, etc.
// To see verbose logs, lower the handler's threshold below zero.
package main

import (
	"context"
	"flag"
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

type LoggingReconciler struct{ client.Client }

func (r *LoggingReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// ctrl.LoggerFrom returns the logger injected by the controller framework,
	// pre-populated with "controller", "controllerGroup", "controllerKind", "name", "namespace".
	logger := ctrl.LoggerFrom(ctx)

	// Attach extra fields that persist for all log lines in this reconcile call.
	logger = logger.WithValues("generation", "unknown")

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("reconciling ConfigMap",
		"dataKeys", len(cm.Data),
		"labels", cm.Labels,
	)

	// Verbose log — V(1) maps to slog level -1, so it is only emitted when the
	// handler threshold is set to -1 or lower (see -verbose below).
	logger.V(1).Info("verbose detail", "resourceVersion", cm.ResourceVersion)

	// Pass enriched logger down to helper via context.
	ctx = ctrl.LoggerInto(ctx, logger)
	r.doWork(ctx, cm)

	return reconcile.Result{}, nil
}

func (r *LoggingReconciler) doWork(ctx context.Context, cm *corev1.ConfigMap) {
	// Helper retrieves the same logger from context — no need to pass logger explicitly.
	ctrl.LoggerFrom(ctx).Info("doing work", "configmap", cm.Name)
}

func main() {
	verbose := flag.Bool("verbose", false, "lower the slog level to -1 so V(1) logs are emitted")
	flag.Parse()

	// Info (V(0)) by default; -verbose drops the threshold to -1 to surface V(1).
	level := slog.LevelInfo
	if *verbose {
		level = slog.Level(-1)
	}

	// slog.NewTextHandler is the console-friendly handler: one key=value line per
	// record. Swap it for slog.NewJSONHandler to emit structured JSON instead.
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	ctrl.SetLogger(logr.FromSlogHandler(handler))
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
		Complete(&LoggingReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}
	setupLog.Info("try: go run ./... -verbose  to enable V(1) logs")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
