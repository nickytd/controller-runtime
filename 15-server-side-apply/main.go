// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 15: Server-side apply (SSA)
//
// Demonstrates:
//   - client.Apply patch type with FieldOwner
//   - How SSA differs from strategic merge patch (field ownership, conflicts)
//   - Force: true to take ownership of conflicting fields
//   - The idempotency guarantee: re-applying identical desired state is a
//     server-side no-op, so a scoped controller settles instead of hot-looping
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	fieldManager = "ssa-lab-controller"
	// labelKey opts a ConfigMap in to this controller. Without scoping, For()
	// would watch *every* ConfigMap in the cluster — including kube-root-ca.crt
	// in every namespace — and writing to each on every reconcile would trigger
	// a watch event that re-enqueues it, producing an unbounded reconcile loop.
	labelKey = "ssa-lab"
)

type SSAReconciler struct{ client.Client }

func (r *SSAReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Build the desired state as an apply configuration. This typed builder is
	// the input to the new client.Apply API: it carries GVK internally (so no
	// TypeMeta juggling) and declares only the fields we own. SSA leaves every
	// other field — and its owner — untouched.
	desired := corev1ac.ConfigMap(req.Name, req.Namespace).
		WithData(map[string]string{
			"managed-by": fieldManager,
			"reconciled": "true",
		})

	// Apply with SSA: only the fields we declare are "owned" by this controller.
	// Other controllers/users can manage other fields without conflict.
	//
	// This is the whole point of the sample: applying identical desired state is
	// a server-side no-op. The API server does not bump resourceVersion when the
	// managed fields already match, so no watch event fires and the controller
	// settles after one pass instead of looping. (The previous version also sent
	// a MergeFrom annotation patch on every reconcile; because that write always
	// produced a new resourceVersion, it re-triggered the watch and the sample
	// hot-looped. Keeping SSA as the single, idempotent writer fixes that.)
	if err := r.Apply(ctx, desired,
		client.FieldOwner(fieldManager),
		client.ForceOwnership, // take ownership of any conflicting fields
	); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("SSA applied", "name", req.Name, "data", desired.Data)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	cfg, _ := ctrl.GetConfig()
	mgr, err := manager.New(cfg, manager.Options{Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}
	// Scope the controller to ConfigMaps that opt in with the ssa-lab label.
	// Without this, For(&ConfigMap{}) watches every ConfigMap in the cluster and
	// the controller would rewrite system objects (kube-root-ca.crt, etc.) in a
	// loop. The predicate filters watch events before they reach the queue.
	onlyLabeled := predicate.NewPredicateFuncs(func(o client.Object) bool {
		_, ok := o.GetLabels()[labelKey]
		return ok
	})

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(onlyLabeled).
		Complete(&SSAReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
