// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 24: Health & readiness probes
//
// Demonstrates:
//   - HealthProbeBindAddress to serve /healthz and /readyz
//   - mgr.AddHealthzCheck with the built-in healthz.Ping checker
//   - mgr.AddReadyzCheck gated on the manager cache being synced
//   - Why liveness (am I alive?) and readiness (can I serve?) differ
package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

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
		HealthProbeBindAddress: ":8081",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Liveness: a simple ping. If the process is running, it is alive.
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}

	// Capture the signal-aware context once — SetupSignalHandler panics if
	// called more than once. Both the readiness check and Start share it, so
	// WaitForCacheSync also unblocks cleanly on shutdown.
	ctx := signals.SetupSignalHandler()

	// Readiness: only ready once the informer cache has synced, so we do not
	// serve (or report ready) while still catching up on existing objects.
	readyCheck := func(_ *http.Request) error {
		if mgr.GetCache().WaitForCacheSync(ctx) {
			return nil
		}
		return errors.New("cache not synced")
	}
	if err := mgr.AddReadyzCheck("cache-sync", readyCheck); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("serving probes on :8081 — try curl localhost:8081/healthz and /readyz")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
