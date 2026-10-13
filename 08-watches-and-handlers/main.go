// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 08: Watches and handlers (below the builder)
//
// Demonstrates:
//   - controller.New() — low-level controller creation without the builder
//   - source.Kind[T]() — typed generic event source (v0.24+ API)
//   - handler.EnqueueRequestForObject — reconcile the changed object
//   - handler.EnqueueRequestForOwner — reconcile the owner when a child changes
//   - handler.EnqueueRequestsFromMapFunc — fan-out to arbitrary objects
//
// Prerequisites: kubectl apply -f widget_crd.yaml
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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

func (w *Widget) DeepCopyObject() runtime.Object     { c := *w; return &c }
func (w *WidgetList) DeepCopyObject() runtime.Object { c := *w; return &c }

type WidgetReconciler struct{ client.Client }

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ctrl.LoggerFrom(ctx).Info("reconciling Widget", "key", req)
	return reconcile.Result{}, nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))
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

	r := &WidgetReconciler{Client: mgr.GetClient()}
	c, err := controller.New("widget-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	// 1. Watch Widgets — reconcile the Widget itself (typed: source.Kind[*Widget])
	if err := c.Watch(source.Kind(mgr.GetCache(), &Widget{},
		&handler.TypedEnqueueRequestForObject[*Widget]{})); err != nil {
		setupLog.Error(err, "unable to watch Widgets")
		os.Exit(1)
	}

	// 2. Watch ConfigMaps — reconcile their owner Widget
	if err := c.Watch(source.Kind(mgr.GetCache(), &corev1.ConfigMap{},
		handler.TypedEnqueueRequestForOwner[*corev1.ConfigMap](
			mgr.GetScheme(), mgr.GetRESTMapper(), &Widget{},
			handler.OnlyControllerOwner(),
		))); err != nil {
		setupLog.Error(err, "unable to watch ConfigMaps")
		os.Exit(1)
	}

	// 3. Watch Namespaces — fan-out: reconcile all Widgets in the namespace
	if err := c.Watch(source.Kind(mgr.GetCache(), &corev1.Namespace{},
		handler.TypedEnqueueRequestsFromMapFunc[*corev1.Namespace](
			func(ctx context.Context, ns *corev1.Namespace) []reconcile.Request {
				widgetList := &WidgetList{}
				if err := mgr.GetClient().List(ctx, widgetList, client.InNamespace(ns.Name)); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, len(widgetList.Items))
				for i, w := range widgetList.Items {
					reqs[i] = reconcile.Request{
						Name: w.Name, Namespace: w.Namespace,
					}
				}
				return reqs
			},
		))); err != nil {
		setupLog.Error(err, "unable to watch Namespaces")
		os.Exit(1)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
