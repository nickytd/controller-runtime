// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 05: Scheme registration and custom CRD
//
// Demonstrates:
//   - Defining a hand-written CRD type (Widget) implementing runtime.Object
//   - Registering it with a Scheme via AddToScheme
//   - Adding the scheme to the Manager so the client knows about it
//   - CRUD on a custom resource using the typed controller client
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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// --- Widget CRD type ---

// GroupVersion is the API group/version for the Widget type.
var GroupVersion = schema.GroupVersion{Group: "example.com", Version: "v1alpha1"}

// SchemeBuilder registers Widget types with a runtime.Scheme. This is the
// apimachinery pattern: a SchemeBuilder is just a list of functions that
// mutate a scheme, and AddToScheme runs them all.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds the Widget types to the given scheme.
var AddToScheme = SchemeBuilder.AddToScheme

// addKnownTypes registers the Widget and WidgetList types under GroupVersion.
func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Widget{}, &WidgetList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

// Widget is a minimal custom resource.
type Widget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              WidgetSpec   `json:"spec"`
	Status            WidgetStatus `json:"status"`
}

type WidgetSpec struct {
	Color string `json:"color"`
	Size  int    `json:"size"`
}

type WidgetStatus struct {
	Ready bool `json:"ready,omitempty"`
}

type WidgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Widget `json:"items"`
}

// DeepCopyInto copies the receiver into out. Spec and Status are all value
// types, but the embedded metav1.ObjectMeta holds maps and slices (Labels,
// Annotations, OwnerReferences, Finalizers, ManagedFields), so a plain struct
// assignment would alias them. We copy the struct, then deep-copy ObjectMeta
// per field. In a real project controller-gen generates this method.
func (w *Widget) DeepCopyInto(out *Widget) {
	*out = *w
	out.TypeMeta = w.TypeMeta
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = w.Spec
	out.Status = w.Status
}

// DeepCopy returns an independent copy of the Widget.
func (w *Widget) DeepCopy() *Widget {
	out := &Widget{}
	w.DeepCopyInto(out)
	return out
}

func (w *Widget) DeepCopyObject() runtime.Object {
	return w.DeepCopy()
}

// DeepCopyInto copies the receiver into out. The Items slice must be copied
// element by element — a plain struct assignment would alias the backing
// array, so mutating a Widget in the copy would corrupt the original. This is
// the aliasing bug a shallow copy hides, and why controller-gen generates
// per-field deep copies.
func (w *WidgetList) DeepCopyInto(out *WidgetList) {
	*out = *w
	out.TypeMeta = w.TypeMeta
	w.ListMeta.DeepCopyInto(&out.ListMeta)
	if w.Items != nil {
		out.Items = make([]Widget, len(w.Items))
		for i := range w.Items {
			w.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns an independent copy of the WidgetList.
func (w *WidgetList) DeepCopy() *WidgetList {
	out := &WidgetList{}
	w.DeepCopyInto(out)
	return out
}

func (w *WidgetList) DeepCopyObject() runtime.Object {
	return w.DeepCopy()
}

// --- Reconciler ---

type WidgetReconciler struct {
	client.Client
}

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	widget := &Widget{}
	if err := r.Get(ctx, req.NamespacedName, widget); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	logger.Info("reconciling Widget", "color", widget.Spec.Color, "size", widget.Spec.Size)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	// Build a scheme that includes core types + our Widget. AddToScheme is the
	// conventional public handle every k8s.io/api package exposes for this.
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		setupLog.Error(err, "unable to add types to scheme")
		os.Exit(1)
	}

	cfg, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}
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

	setupLog.Info("watching Widgets — apply widget_crd.yaml first")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
