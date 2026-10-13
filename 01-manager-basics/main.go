// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 01: Manager basics
//
// Demonstrates:
//   - manager.New() with Options (scheme, metrics, health probes)
//   - ctrl.SetLogger with a slog-backed logger for the example
//   - Registering a custom Runnable via mgr.Add()
//   - Signal-aware Start(ctx) for graceful shutdown
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// ticker is a simple Runnable that logs once per second.
type ticker struct{}

// compile-time check that *ticker is a manager.Runnable. It is handed to
// mgr.Add, which accepts the interface, so this pins the contract at the type.
var _ manager.Runnable = (*ticker)(nil)

func (t *ticker) Start(ctx context.Context) error {
	logger := log.FromContext(ctx).WithName("ticker")
	logger.Info("ticker started")
	<-ctx.Done()
	logger.Info("ticker stopped")
	return nil
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
		// Disable metrics and health servers for this example
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register our custom Runnable — started alongside controllers
	if err := mgr.Add(&ticker{}); err != nil {
		setupLog.Error(err, "unable to add ticker runnable")
		os.Exit(1)
	}

	setupLog.Info("starting manager — press Ctrl+C to stop")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
