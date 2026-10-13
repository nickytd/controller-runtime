// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 10: Status subresource
//
// Demonstrates:
//   - client.Status().Update() — write only the /status subresource
//   - Why Status().Update() is separate from Update() (different RBAC, no spec drift)
//   - Reading status back from the cache after a status update
//   - CRD must declare `subresources: status: {}` in its schema
//
// Prerequisites: kubectl apply -f widget_crd.yaml
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

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
	Spec              WidgetSpec   `json:"spec"`
	Status            WidgetStatus `json:"status"`
}

type WidgetSpec struct {
	Color string `json:"color"`
}

type WidgetStatus struct {
	Ready   bool   `json:"ready,omitempty"`
	Message string `json:"message,omitempty"`
}

type WidgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Widget `json:"items"`
}

func (w *Widget) DeepCopyObject() runtime.Object     { c := *w; return &c }
func (w *WidgetList) DeepCopyObject() runtime.Object { c := *w; return &c }

type WidgetReconciler struct{ client.Client }

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	widget := &Widget{}
	if err := r.Get(ctx, req.NamespacedName, widget); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Perform "work" — here we just mark it ready
	original := widget.DeepCopyObject().(*Widget)
	widget.Status.Ready = true
	widget.Status.Message = fmt.Sprintf("color %q is valid", widget.Spec.Color)

	// Status().Update() writes ONLY the status subresource.
	// A plain r.Update() would overwrite spec too and conflict with other actors.
	if err := r.Status().Update(ctx, widget); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("status updated",
		"was", original.Status.Ready,
		"now", widget.Status.Ready,
		"message", widget.Status.Message,
	)
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

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&Widget{}).
		Complete(&WidgetReconciler{Client: mgr.GetClient()}); err != nil {
		setupLog.Error(err, "unable to set up Widget controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
