// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 06: Owns and owned resources
//
// Demonstrates:
//   - ctrl.SetControllerReference — set an owner on a child object
//   - .Owns() on the builder — watch child ConfigMaps and reconcile the owner Widget
//   - controllerutil.CreateOrUpdate — idempotent child management
//   - Cascading reconcile: when child changes, parent Widget is re-queued
//
// Prerequisites: kubectl apply -f widget_crd.yaml
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	Spec              struct {
		Color string `json:"color"`
	} `json:"spec"`
}

type WidgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Widget `json:"items"`
}

func (w *Widget) DeepCopyObject() runtime.Object {
	c := *w
	w.ObjectMeta.DeepCopyInto(&c.ObjectMeta)
	return &c
}

func (w *WidgetList) DeepCopyObject() runtime.Object {
	c := *w
	w.ListMeta.DeepCopyInto(&c.ListMeta)
	if w.Items != nil {
		c.Items = make([]Widget, len(w.Items))
		for i := range w.Items {
			c.Items[i] = *w.Items[i].DeepCopyObject().(*Widget)
		}
	}
	return &c
}

type WidgetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := ctrl.LoggerFrom(ctx)

	widget := &Widget{}
	if err := r.Get(ctx, req.NamespacedName, widget); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Desired child ConfigMap
	cm := &corev1.ConfigMap{
		Name:      widget.Name + "-config",
		Namespace: widget.Namespace,
	}

	// SetControllerReference links cm to widget — deletion cascades
	if err := ctrl.SetControllerReference(widget, cm, r.Scheme); err != nil {
		return reconcile.Result{}, err
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{"color": widget.Spec.Color}
		return nil
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return reconcile.Result{}, err
	}
	logger.Info("child ConfigMap", "result", result)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))

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
		Owns(&corev1.ConfigMap{}). // re-queue Widget when its owned ConfigMap changes
		Complete(&WidgetReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}); err != nil {
		setupLog.Error(err, "unable to set up Widget controller")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
