// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 18: Validating admission webhook
//
// Demonstrates:
//   - admission.Validator[T] interface: ValidateCreate / ValidateUpdate / ValidateDelete
//   - Registering the webhook via WebhookManagedBy builder
//   - webhook.Server TLS setup (cert/key injected by cert-manager or envtest)
//   - Returning field-level validation errors with field.ErrorList
//
// Prerequisites:
//
//	kubectl apply -f widget_crd.yaml
//	Set KUBEBUILDER_ASSETS and run via envtest for TLS (see case 23)
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
	"k8s.io/apimachinery/pkg/util/validation/field"
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

// WidgetValidator implements admission.Validator[*Widget].
type WidgetValidator struct{}

// compile-time check that *WidgetValidator satisfies the typed Validator. The
// webhook builder consumes this via WithValidator, so a signature drift would
// otherwise only surface at that call site.
var _ admission.Validator[*Widget] = (*WidgetValidator)(nil)

func (v *WidgetValidator) ValidateCreate(_ context.Context, obj *Widget) (admission.Warnings, error) {
	return v.validate(obj)
}

func (v *WidgetValidator) ValidateUpdate(_ context.Context, _, newObj *Widget) (admission.Warnings, error) {
	return v.validate(newObj)
}

func (v *WidgetValidator) ValidateDelete(_ context.Context, _ *Widget) (admission.Warnings, error) {
	return nil, nil
}

var validColors = map[string]bool{"red": true, "green": true, "blue": true}

func (v *WidgetValidator) validate(w *Widget) (admission.Warnings, error) {
	var errs field.ErrorList
	if !validColors[w.Spec.Color] {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "color"), w.Spec.Color,
			"must be one of: red, green, blue",
		))
	}
	if w.Spec.Size < 1 || w.Spec.Size > 10 {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "size"), w.Spec.Size,
			"must be between 1 and 10",
		))
	}
	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
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
			CertDir: os.Getenv("WEBHOOK_CERT_DIR"), // set by envtest or cert-manager
		}),
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register validating webhook — path: /validate-example-com-v1alpha1-widget
	// v0.24+: WebhookManagedBy is a typed generic function
	if err := ctrl.NewWebhookManagedBy[*Widget](mgr, &Widget{}).
		WithValidator(&WidgetValidator{}).
		Complete(); err != nil {
		setupLog.Error(err, "unable to set up webhook")
		os.Exit(1)
	}

	setupLog.Info("validating webhook registered at /validate-example-com-v1alpha1-widget")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
