// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 04: Result and requeue strategies
//
// Demonstrates:
//   - reconcile.Result{} — success, no requeue
//   - reconcile.Result{RequeueAfter: d} — requeue after fixed delay
//   - Returning a non-nil error — exponential backoff requeue
//   - reconcile.TerminalError(err) — permanent failure, no retry
//   - Counting reconcile attempts via a simple in-memory counter
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// demoName is the single ConfigMap this controller reconciles. Filtering to one
// object keeps the requeue demonstration legible — otherwise every unrelated
// ConfigMap in the cluster (kube-root-ca.crt, leases, …) drives the counter.
const demoName = "requeue-demo"

type RequeueReconciler struct {
	client.Client
	attempts atomic.Int64
}

var errTransient = errors.New("transient error — will backoff and retry")

func (r *RequeueReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	attempt := r.attempts.Add(1)

	switch {
	case attempt <= 2:
		// Return an error → exponential backoff requeue
		logger.Info("attempt failed, will backoff", "attempt", attempt)
		return reconcile.Result{}, errTransient

	case attempt == 3:
		// Periodic requeue independent of events
		logger.Info("requeueing after delay", "attempt", attempt)
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil

	case attempt == 4:
		// Terminal error — controller gives up, no further retries
		logger.Info("terminal failure, giving up", "attempt", attempt)
		return reconcile.Result{}, reconcile.TerminalError(errors.New("unrecoverable"))

	default:
		// Normal success
		logger.Info("reconciled successfully", "attempt", attempt)
		return reconcile.Result{}, nil
	}
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
		For(&corev1.ConfigMap{}, builder.WithPredicates(
			predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == demoName
			}),
		)).
		Complete(&RequeueReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up requeue controller")
		os.Exit(1)
	}
	setupLog.Info("create a ConfigMap named "+demoName+" in default namespace to trigger the requeue demo", "configmap", demoName)
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
