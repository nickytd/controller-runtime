// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 20: Conversion webhook (multi-version CRD)
//
// Demonstrates:
//   - Hub-spoke version conversion pattern
//   - conversion.Convertible interface: ConvertTo / ConvertFrom
//   - conversion.Hub interface: marks the storage version
//   - Registering the conversion webhook via WebhookManagedBy
//   - CRD must have `conversion.strategy: Webhook` in its spec
//
// In this example v1alpha1 is the hub (storage version), v1beta1 is a spoke.
//
// Prerequisites: kubectl apply -f widget_crd.yaml
package main

import (
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// --- v1alpha1 (hub / storage version) ---

var GVv1alpha1 = schema.GroupVersion{Group: "example.com", Version: "v1alpha1"}
var SchemeBuilderV1 = runtime.NewSchemeBuilder(addV1AlphaTypes)

func addV1AlphaTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GVv1alpha1, &Widget{}, &WidgetList{})
	metav1.AddToGroupVersion(s, GVv1alpha1)
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

// Widget's spec is all scalars with no pointers, so a struct assignment is a
// complete, independent copy — no DeepCopyInto/DeepCopy helpers needed.
func (w *Widget) DeepCopyObject() runtime.Object { c := *w; return &c }

// DeepCopyObject copies the WidgetList. The one real deep copy here: a shallow
// struct assignment would alias the Items backing array, so allocate a fresh
// slice. Its elements have no pointers, so copy() of them is enough.
func (w *WidgetList) DeepCopyObject() runtime.Object {
	c := *w
	if w.Items != nil {
		c.Items = make([]Widget, len(w.Items))
		copy(c.Items, w.Items)
	}
	return &c
}

// Hub marks v1alpha1 as the storage (hub) version.
func (w *Widget) Hub() {}

// compile-time check that *Widget is the conversion hub. Convertibility is
// detected reflectively at webhook registration, so a missing/renamed method
// would silently disable conversion rather than fail to compile.
var _ conversion.Hub = (*Widget)(nil)

// --- v1beta1 (spoke version) ---

var GVv1beta1 = schema.GroupVersion{Group: "example.com", Version: "v1beta1"}
var SchemeBuilderV1Beta = runtime.NewSchemeBuilder(addV1BetaTypes)

func addV1BetaTypes(s *runtime.Scheme) error {
	// Register under Kind "Widget" (not the Go type name "WidgetV1Beta1"): both
	// versions must share the same GroupKind for the API server and the
	// conversion machinery to treat them as two versions of one resource. Using
	// AddKnownTypes here would register Kind "WidgetV1Beta1", so IsConvertible
	// would see a single-version GroupKind and never enable the webhook.
	s.AddKnownTypeWithName(GVv1beta1.WithKind("Widget"), &WidgetV1Beta1{})
	s.AddKnownTypeWithName(GVv1beta1.WithKind("WidgetList"), &WidgetV1Beta1List{})
	metav1.AddToGroupVersion(s, GVv1beta1)
	return nil
}

// WidgetV1Beta1 is a spoke — it renames "size" to "magnitude".
type WidgetV1Beta1 struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              WidgetV1Beta1Spec `json:"spec"`
}

type WidgetV1Beta1Spec struct {
	Color     string `json:"color"`
	Magnitude int    `json:"magnitude"` // renamed from "size"
}

type WidgetV1Beta1List struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []WidgetV1Beta1 `json:"items"`
}

// WidgetV1Beta1Spec is all scalars with no pointers — shallow copy suffices.
func (w *WidgetV1Beta1) DeepCopyObject() runtime.Object { c := *w; return &c }

// DeepCopyObject copies the list, allocating a fresh Items slice to avoid
// aliasing the original's backing array.
func (w *WidgetV1Beta1List) DeepCopyObject() runtime.Object {
	c := *w
	if w.Items != nil {
		c.Items = make([]WidgetV1Beta1, len(w.Items))
		copy(c.Items, w.Items)
	}
	return &c
}

// ConvertTo converts v1beta1 → v1alpha1 (hub).
func (src *WidgetV1Beta1) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*Widget)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec.Color = src.Spec.Color
	dst.Spec.Size = src.Spec.Magnitude // rename back
	return nil
}

// ConvertFrom converts v1alpha1 (hub) → v1beta1.
func (dst *WidgetV1Beta1) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*Widget)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec.Color = src.Spec.Color
	dst.Spec.Magnitude = src.Spec.Size // rename
	return nil
}

// compile-time check that *WidgetV1Beta1 is a conversion spoke. A wrong
// ConvertTo/ConvertFrom signature would otherwise leave the type silently
// non-convertible and the webhook unregistered.
var _ conversion.Convertible = (*WidgetV1Beta1)(nil)

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilderV1.AddToScheme(s))
	utilruntime.Must(SchemeBuilderV1Beta.AddToScheme(s))

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

	// Register conversion webhook for the spoke type
	// v0.24+: WebhookManagedBy is a typed generic function
	if err := ctrl.NewWebhookManagedBy(mgr, &WidgetV1Beta1{}).
		Complete(); err != nil {
		setupLog.Error(err, "unable to set up webhook")
		os.Exit(1)
	}

	setupLog.Info("conversion webhook registered — v1alpha1 (hub) ↔ v1beta1 (spoke)")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
