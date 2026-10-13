// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 16: Custom metrics
//
// Demonstrates:
//   - metrics.Registry — controller-runtime's Prometheus registry
//   - Registering custom Counter, Gauge, and Histogram in a reconciler
//   - mgr.Options.MetricsBindAddress — expose /metrics endpoint
//   - Scraping metrics: curl http://localhost:8080/metrics
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	reconcileTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "widget_reconcile_total",
		Help: "Total number of reconcile calls.",
	}, []string{"result"})

	reconcileErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "widget_reconcile_errors_total",
		Help: "Total number of reconcile errors.",
	})

	activeCMs = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "widget_active_configmaps",
		Help: "Number of ConfigMaps currently tracked.",
	})

	reconcileDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "widget_reconcile_duration_seconds",
		Help:    "Reconcile call duration.",
		Buckets: prometheus.DefBuckets,
	})
)

func init() {
	// Register with the controller-runtime shared registry (not the default prometheus one)
	metrics.Registry.MustRegister(reconcileTotal, reconcileErrors, activeCMs, reconcileDuration)
}

type MetricsReconciler struct{ client.Client }

func (r *MetricsReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	timer := prometheus.NewTimer(reconcileDuration)
	defer timer.ObserveDuration()

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
		if apierrors.IsNotFound(err) {
			reconcileTotal.WithLabelValues("not_found").Inc()
			return reconcile.Result{}, nil
		}
		// A real error (network, RBAC, etc.) — count it and requeue.
		reconcileErrors.Inc()
		reconcileTotal.WithLabelValues("error").Inc()
		return reconcile.Result{}, err
	}

	// Count active ConfigMaps
	cmList := &corev1.ConfigMapList{}
	if err := r.List(ctx, cmList, client.InNamespace(req.Namespace)); err == nil {
		activeCMs.Set(float64(len(cmList.Items)))
	}

	reconcileTotal.WithLabelValues("success").Inc()
	ctrl.LoggerFrom(ctx).Info("reconciled", "name", req.Name)
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
		// Expose metrics on :8080/metrics — scrape with: curl http://localhost:8080/metrics
		Metrics:                metricsserver.Options{BindAddress: ":8080"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(&MetricsReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}
	setupLog.Info("metrics available at http://localhost:8080/metrics")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
