// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 27: Reconciler panic recovery via controller.Options.RecoverPanic
//
// Demonstrates:
//   - controller.Options.RecoverPanic (*bool) toggling whether a panic inside
//     Reconcile is recovered and turned into a requeue-with-error, or allowed to
//     crash the whole process
//   - Default behavior: RecoverPanic defaults to true when unset (both on the
//     controller option and the Manager-level Controller.RecoverPanic)
//
// Run, then create a ConfigMap named "boom" to trigger the panic:
//
//	kubectl create configmap boom --from-literal=k=v -n default
//
// With recovery on (default/this case) the panic is logged and the item is
// requeued with backoff; the process keeps running. Flip the flag to false to
// watch the process exit instead.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Reconciler panics when it sees a ConfigMap named "boom", and is otherwise a no-op.
type Reconciler struct{}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	if req.Name == "boom" {
		logger.Info("about to panic", "name", req.Name)
		panic("intentional panic from reconciler: object named 'boom'")
	}
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

	// RecoverPanic is a *bool. true (the default) converts a reconciler panic into
	// a logged error + requeue so one bad object cannot take down the controller.
	// Set this to false to let the panic propagate and crash the process instead.
	recoverPanic := true

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithOptions(controller.Options{
			RecoverPanic: &recoverPanic,
		}).
		Complete(&Reconciler{}); err != nil {
		setupLog.Error(err, "unable to set up controller")
		os.Exit(1)
	}

	setupLog.Info("watching ConfigMaps with RecoverPanic — create a ConfigMap named 'boom' to trigger a recovered panic")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
