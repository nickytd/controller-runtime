// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 09: External events via source.Channel
//
// Demonstrates:
//   - source.Channel() — inject reconcile requests from outside Kubernetes
//   - GenericEvent carrying a synthetic object
//   - A time.Ticker driving periodic reconciliation independent of API events
//   - Use case: polling an external system, responding to webhooks, cron-style work
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type TickReconciler struct{ client.Client }

func (r *TickReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("tick reconcile", "key", req.NamespacedName)
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

	// Channel that receives GenericEvents from our ticker goroutine
	ch := make(chan event.GenericEvent, 10)

	c, err := controller.New("tick-controller", mgr, controller.Options{
		Reconciler: &TickReconciler{Client: mgr.GetClient()},
	})
	if err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	// source.Channel maps each GenericEvent to a reconcile.Request via the handler
	if err := c.Watch(source.Channel(ch, &handler.EnqueueRequestForObject{})); err != nil {
		setupLog.Error(err, "unable to watch channel source")
		os.Exit(1)
	}

	// Ticker goroutine — sends a synthetic ConfigMap event every 5 seconds
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ch <- event.GenericEvent{
				Object: &corev1.ConfigMap{
					Name:      "external-trigger",
					Namespace: "default",
				},
			}
			ctrl.Log.Info("external event injected")
		}
	}()

	setupLog.Info("controller running — injecting a tick every 5 seconds")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
