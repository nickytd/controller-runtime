// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 14: Leader election
//
// Demonstrates:
//   - manager.Options.LeaderElection / LeaderElectionID / LeaderElectionNamespace
//   - How the manager blocks Start() until leadership is acquired
//   - LeaderElectionRunnable — only runs on the leader (NeedLeaderElection() = true)
//   - Non-leader Runnable — runs on all replicas (NeedLeaderElection() = false)
//   - Elected() channel — notified when this instance becomes leader
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// leaderWork only runs on the elected leader.
type leaderWork struct{}

func (l *leaderWork) Start(ctx context.Context) error {
	ctrl.Log.Info("LeaderWork: I am the leader, doing exclusive work")
	<-ctx.Done()
	ctrl.Log.Info("LeaderWork: lost leadership or shutting down")
	return nil
}

func (l *leaderWork) NeedLeaderElection() bool { return true }

// healthProbe runs on every replica regardless of leadership.
type healthProbe struct{}

func (h *healthProbe) Start(ctx context.Context) error {
	ctrl.Log.Info("HealthProbe: running on all replicas")
	<-ctx.Done()
	return nil
}

func (h *healthProbe) NeedLeaderElection() bool { return false }

// compile-time checks: both are manager.Runnables whose NeedLeaderElection
// opts them in or out of leader-gated execution (manager.LeaderElectionRunnable).
var (
	_ manager.Runnable               = (*leaderWork)(nil)
	_ manager.LeaderElectionRunnable = (*leaderWork)(nil)
	_ manager.Runnable               = (*healthProbe)(nil)
	_ manager.LeaderElectionRunnable = (*healthProbe)(nil)
)

// ConfigMapReconciler only reconciles on the leader (controllers default to NeedLeaderElection=true).
type ConfigMapReconciler struct{}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("reconciling (leader only)", "key", req)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	cfg, _ := ctrl.GetConfig()

	mgr, err := manager.New(cfg, manager.Options{
		Metrics:                    metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress:     "0",
		LeaderElection:             true,
		LeaderElectionID:           "ctrl-runtime-lab-leader",
		LeaderElectionNamespace:    "default",
		LeaderElectionResourceLock: "leases",
		LeaseDuration:              nil, // use defaults (15s)
		RenewDeadline:              nil, // use defaults (10s)
		RetryPeriod:                nil, // use defaults (2s)
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	_ = mgr.Add(&leaderWork{})
	_ = mgr.Add(&healthProbe{})

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(&ConfigMapReconciler{}); err != nil {
		setupLog.Error(err, "unable to set up ConfigMap controller")
		os.Exit(1)
	}

	setupLog.Info("starting with leader election — run multiple replicas to observe failover")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
