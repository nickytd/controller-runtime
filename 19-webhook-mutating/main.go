// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 19: Mutating admission webhook (defaulter)
//
// Demonstrates:
//   - admission.Defaulter[T] interface: Default()
//   - Registering the webhook via WithDefaulter on the builder
//   - Setting default values before the object is persisted
//   - Execution order: defaulter runs BEFORE validator
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
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
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
	Spec              WidgetSpec `json:"spec"`
}

type WidgetSpec struct {
	Color string `json:"color"`
	Size  int    `json:"size"`
}

type WidgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Widget `json:"items"`
}

func (w *Widget) DeepCopyObject() runtime.Object     { c := *w; return &c }
func (w *WidgetList) DeepCopyObject() runtime.Object { c := *w; return &c }

// WidgetDefaulter implements admission.Defaulter[*Widget].
type WidgetDefaulter struct{}

// compile-time check that *WidgetDefaulter satisfies the typed Defaulter. The
// webhook builder consumes this via WithDefaulter, so a signature drift would
// otherwise only surface at that call site.
var _ admission.Defaulter[*Widget] = (*WidgetDefaulter)(nil)

// Default sets missing fields before the object is stored.
// The framework serialises the mutation as a JSON patch and returns it to the API server.
func (d *WidgetDefaulter) Default(_ context.Context, w *Widget) error {
	logger := ctrl.Log.WithName("widget-defaulter")
	if w.Spec.Color == "" {
		w.Spec.Color = "blue"
		logger.Info("defaulted color", "name", w.Name)
	}
	if w.Spec.Size == 0 {
		w.Spec.Size = 5
		logger.Info("defaulted size", "name", w.Name)
	}
	if w.Labels == nil {
		w.Labels = map[string]string{}
	}
	w.Labels["defaulted-by"] = "widget-defaulter"
	return nil
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
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: os.Getenv("WEBHOOK_CERT_DIR"),
		}),
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register mutating webhook — path: /mutate-example-com-v1alpha1-widget
	// v0.24+: WebhookManagedBy is a typed generic function
	if err := ctrl.NewWebhookManagedBy[*Widget](mgr, &Widget{}).
		WithDefaulter(&WidgetDefaulter{}).
		Complete(); err != nil {
		setupLog.Error(err, "unable to set up webhook")
		os.Exit(1)
	}

	setupLog.Info("mutating webhook registered at /mutate-example-com-v1alpha1-widget")
	setupLog.Info("try creating a Widget without spec.color — the defaulter will set it to 'blue'")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
