// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 11: Structured finalizers
//
// Demonstrates:
//   - pkg/finalizer.NewFinalizers().Register() — named finalizer management
//   - finalizer.Finalize() called from reconciler — auto adds/removes finalizer string
//   - Cleanup logic runs only when DeletionTimestamp is set
//   - Contrast with manual finalizer string manipulation
//
// Prerequisites: kubectl apply -f widget_crd.yaml
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/finalizer"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const widgetFinalizer = "example.com/widget-cleanup"

var GroupVersion = schema.GroupVersion{Group: "example.com", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Widget{}, &WidgetList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

type Widget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              struct {
		Color string `json:"color"`
	} `json:"spec"`
}
type WidgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Widget `json:"items"`
}

func (w *Widget) DeepCopyObject() runtime.Object     { c := *w; return &c }
func (w *WidgetList) DeepCopyObject() runtime.Object { c := *w; return &c }

// widgetCleanup implements finalizer.Finalizer.
type widgetCleanup struct{}

// compile-time check that *widgetCleanup satisfies finalizer.Finalizer. It is
// registered via finalizer.NewFinalizers().Register, which takes the interface.
var _ finalizer.Finalizer = (*widgetCleanup)(nil)

func (f *widgetCleanup) Finalize(ctx context.Context, obj client.Object) (finalizer.Result, error) {
	widget := obj.(*Widget)
	ctrl.LoggerFrom(ctx).Info("running cleanup for Widget", "name", widget.Name, "color", widget.Spec.Color)
	// Real cleanup: delete external resources, revoke credentials, etc.
	return finalizer.Result{}, nil
}

type WidgetReconciler struct {
	client.Client
	finalizers finalizer.Finalizers
}

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	widget := &Widget{}
	if err := r.Get(ctx, req.NamespacedName, widget); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Finalize handles both registration (on live objects) and cleanup (on deleting objects).
	result, err := r.finalizers.Finalize(ctx, widget)
	if err != nil {
		return reconcile.Result{}, err
	}
	if result.Updated {
		// finalizer string was added/removed — persist the change
		if err := r.Update(ctx, widget); err != nil {
			return reconcile.Result{}, err
		}
		logger.Info("finalizer state updated")
	}

	if widget.DeletionTimestamp.IsZero() {
		logger.Info("reconciling live Widget", "color", widget.Spec.Color)
	}
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))

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

	finalizers := finalizer.NewFinalizers()
	if err := finalizers.Register(widgetFinalizer, &widgetCleanup{}); err != nil {
		setupLog.Error(err, "unable to register finalizer")
		os.Exit(1)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&Widget{}).
		Complete(&WidgetReconciler{
			Client:     mgr.GetClient(),
			finalizers: finalizers,
		}); err != nil {
		setupLog.Error(err, "unable to set up Widget controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
