// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 07: Predicates
//
// Demonstrates:
//   - predicate.GenerationChangedPredicate — skip status-only updates
//   - predicate.And / predicate.Or — composing predicates
//   - Custom predicate via predicate.NewPredicateFuncs
//   - predicate.Funcs with per-event-type logic (DeleteFunc)
//   - WithEventFilter on the builder — applies to the primary For() resource
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type PredicateReconciler struct{ client.Client }

func (r *PredicateReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("reconcile triggered (passed all predicates)", "key", req)
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

	// Custom predicate: only reconcile ConfigMaps in the "default" namespace
	onlyDefault := predicate.NewPredicateFuncs(func(o client.Object) bool {
		return o.GetNamespace() == "default"
	})

	// Compose: GenerationChanged AND in default namespace.
	// This means: skip status-only writes AND skip other namespaces.
	//
	// We also reject deletes on this arm. GenerationChangedPredicate only
	// overrides Update — its Delete inherits predicate.Funcs and defaults to
	// true. onlyDefault's DeleteFunc also returns true for the default
	// namespace. So without the explicit DeleteFunc below, `combined.Delete`
	// would return true for any default-namespace delete, and because this arm
	// comes first in the Or, the Or would short-circuit before ever calling
	// logDeletes — the delete log would never fire. Rejecting deletes here
	// forces the Or to fall through to the delete-aware arm.
	combined := predicate.And(
		predicate.GenerationChangedPredicate{},
		onlyDefault,
		predicate.Funcs{
			DeleteFunc: func(event.DeleteEvent) bool { return false },
		},
	)

	// Delete-aware arm: logs every delete and lets it through to reconcile.
	// This is the only arm that handles deletes, so the side effect runs for
	// every delete regardless of namespace.
	logDeletes := predicate.Funcs{
		DeleteFunc: func(e event.DeleteEvent) bool {
			ctrl.Log.Info("delete event seen", "name", e.Object.GetName())
			return true // still reconcile deletes
		},
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.Or(combined, logDeletes)).
		Complete(&PredicateReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
