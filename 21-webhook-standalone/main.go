// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 21: Standalone webhook (no Manager)
//
// Demonstrates:
//   - admission.WithDefaulter — build a net/http webhook handler without a Manager
//   - admission.Defaulter[runtime.Object] — the untyped form takes runtime.Object
//     (type-assert to your concrete type), handy for a generic standalone handler
//   - webhook.Server for TLS — useful for sidecar injection or custom webhook servers
//   - Decoder usage for custom handlers via admission.Request
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
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
	Spec              struct {
		Color string `json:"color"`
	} `json:"spec"`
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

// standaloneDefaulter implements admission.Defaulter[runtime.Object].
// Note: Default receives runtime.Object — type-assert to your concrete type.
type standaloneDefaulter struct{}

// compile-time check that *standaloneDefaulter satisfies the untyped defaulter.
// It is passed to WithCustomDefaulter, so a signature drift would otherwise only
// fail there.
var _ admission.Defaulter[runtime.Object] = (*standaloneDefaulter)(nil)

func (d *standaloneDefaulter) Default(_ context.Context, obj runtime.Object) error {
	w, ok := obj.(*Widget)
	if !ok {
		return nil
	}
	if w.Spec.Color == "" {
		w.Spec.Color = "green"
		ctrl.Log.Info("standalone defaulter: set color to green", "name", w.Name)
	}
	return nil
}

func main() {
	ctrl.SetLogger(logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	setupLog := ctrl.Log.WithName("setup")

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))

	// Build a standalone HTTP handler — no Manager required.
	// WithCustomDefaulter takes the untyped Defaulter[runtime.Object] form plus a
	// prototype object (&Widget{}) it clones per request to decode into. (The
	// generic WithDefaulter[T] can't be used with T = runtime.Object: it builds
	// instances via reflect.TypeOf on the zero value, which is nil for an
	// interface and panics. WithCustomDefaulter exists for exactly this case.)
	mutateHandler := admission.WithCustomDefaulter(s, &Widget{}, &standaloneDefaulter{})

	// Wire directly into net/http
	mux := http.NewServeMux()
	mux.Handle("/mutate-widget", mutateHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	certDir := os.Getenv("WEBHOOK_CERT_DIR")
	if certDir == "" {
		certDir = "/tmp/webhook-certs"
	}

	srv := webhook.NewServer(webhook.Options{
		Port:    9444, // different port to avoid conflict with cases 18-20
		CertDir: certDir,
		Host:    "0.0.0.0",
	})

	setupLog.Info("standalone webhook server configured", "addr", ":9444/mutate-widget", "certDir", certDir)

	// In a real deployment, register the handler on the webhook.Server and call Start.
	// Here we show the wiring — integrate with your own TLS server to serve requests.
	_ = srv
	_ = mux
	setupLog.Info("handler registered — integrate with your own TLS server to serve requests")
}
