// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 13: Field indexer
//
// Demonstrates:
//   - mgr.GetFieldIndexer().IndexField() — register a custom index at startup
//   - client.MatchingFields{} — use the index for fast List queries
//   - Index Pods by their spec.nodeName field
//   - Why field indexes are faster than filtering in the reconciler loop
//
// Prerequisites: none — this case indexes built-in Pods and watches built-in Nodes.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const podNodeNameIndex = ".spec.nodeName"

type NodeReconciler struct{ client.Client }

func (r *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	// Fast lookup: all Pods on this node, using the pre-built index
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.MatchingFields{podNodeNameIndex: req.Name},
	); err != nil {
		return reconcile.Result{}, err
	}
	logger.Info("pods on node", "node", req.Name, "count", len(podList.Items))
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))

	cfg, _ := ctrl.GetConfig()
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 s,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register the index before starting the manager
	if err := mgr.GetFieldIndexer().IndexField(context.Background(),
		&corev1.Pod{},
		podNodeNameIndex,
		func(obj client.Object) []string {
			pod := obj.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		},
	); err != nil {
		setupLog.Error(err, "unable to set up field indexer")
		os.Exit(1)
	}

	// Reconcile Nodes — on each reconcile, list all Pods on that node via the index
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(&NodeReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up Node controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
