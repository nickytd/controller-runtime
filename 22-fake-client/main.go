// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 22: Unit testing with fake client
//
// Demonstrates:
//   - fake.NewClientBuilder().WithObjects().Build() — in-memory client
//   - WithStatusSubresource() — enable status writes on specific types
//   - Testing a reconciler without a real cluster or manager
//   - Asserting side effects: read back objects the reconciler created/updated
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// --- Widget CRD type (same as other cases) ---

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
	Ready bool `json:"ready,omitempty"`
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

// --- Reconciler under test ---

type WidgetReconciler struct{ client.Client }

func (r *WidgetReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	widget := &Widget{}
	if err := r.Get(ctx, req.NamespacedName, widget); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Create a child ConfigMap
	cm := &corev1.ConfigMap{
		Name:      widget.Name + "-config",
		Namespace: widget.Namespace,
		Data:      map[string]string{"color": widget.Spec.Color},
	}
	if err := r.Create(ctx, cm); client.IgnoreAlreadyExists(err) != nil {
		return reconcile.Result{}, err
	}

	// Update status
	widget.Status.Ready = true
	return reconcile.Result{}, r.Status().Update(ctx, widget)
}

// --- Checks ---

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

func checkReconcileCreatesConfigMap() error {
	s := buildScheme()
	widget := &Widget{
		Name: "my-widget", Namespace: "default",
		Spec: WidgetSpec{Color: "red"},
	}

	// Seed the fake client with the Widget; enable status writes on Widget
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(widget).
		WithStatusSubresource(&Widget{}).
		Build()

	r := &WidgetReconciler{Client: fakeClient}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: "my-widget", Namespace: "default",
	})
	if err != nil {
		return fmt.Errorf("reconcile error: %w", err)
	}

	// Assert child ConfigMap was created
	cm := &corev1.ConfigMap{}
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Name: "my-widget-config", Namespace: "default"}, cm); err != nil {
		return fmt.Errorf("expected ConfigMap to exist: %w", err)
	}
	if cm.Data["color"] != "red" {
		return fmt.Errorf("expected color=red, got %s", cm.Data["color"])
	}
	slog.Info("check passed", "name", "ReconcileCreatesConfigMap", "configMap", cm.Name, "color", cm.Data["color"])
	return nil
}

func checkReconcileSetsStatusReady() error {
	s := buildScheme()
	widget := &Widget{
		Name: "my-widget", Namespace: "default",
		Spec: WidgetSpec{Color: "blue"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(widget).
		WithStatusSubresource(&Widget{}).
		Build()

	r := &WidgetReconciler{Client: fakeClient}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: "my-widget", Namespace: "default",
	})
	if err != nil {
		return fmt.Errorf("reconcile error: %w", err)
	}

	updated := &Widget{}
	if err := fakeClient.Get(context.Background(),
		types.NamespacedName{Name: "my-widget", Namespace: "default"}, updated); err != nil {
		return fmt.Errorf("expected Widget to exist: %w", err)
	}
	if !updated.Status.Ready {
		return fmt.Errorf("expected status.ready=true")
	}
	slog.Info("check passed", "name", "ReconcileSetsStatusReady", "widget", updated.Name, "ready", updated.Status.Ready)
	return nil
}

func checkReconcileNotFound() error {
	s := buildScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	r := &WidgetReconciler{Client: fakeClient}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: "missing", Namespace: "default",
	})
	if err != nil {
		return fmt.Errorf("expected no error on not-found, got: %w", err)
	}
	slog.Info("check passed", "name", "ReconcileNotFound")
	return nil
}

// main runs the checks directly so `go run ./...` exercises the reconciler
// against the fake client without a real cluster or Manager.
func main() {
	checks := []func() error{
		checkReconcileCreatesConfigMap,
		checkReconcileSetsStatusReady,
		checkReconcileNotFound,
	}
	for _, check := range checks {
		if err := check(); err != nil {
			slog.Error("check failed", "err", err)
			os.Exit(1)
		}
	}
	slog.Info("all checks passed")
}
