// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 23: Integration testing with envtest
//
// Demonstrates:
//   - envtest.Environment — real etcd + kube-apiserver, no cluster needed
//   - envtest.CRDInstallOptions — load CRD YAMLs before tests run
//   - Running a full Manager + reconciler inside a test
//   - Asserting reconciler behaviour against the real API server
//   - Proper setup/teardown with TestMain
//
// Run with:
//
//	export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
//	go test ./... -v
package reconciler_test

import (
	"context"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// --- Widget CRD types ---

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
	cm := &corev1.ConfigMap{
		Name: widget.Name + "-config", Namespace: widget.Namespace,
		Data: map[string]string{"color": widget.Spec.Color},
	}
	_ = ctrl.SetControllerReference(widget, cm, r.Scheme())
	if err := r.Create(ctx, cm); client.IgnoreAlreadyExists(err) != nil {
		return reconcile.Result{}, err
	}
	widget.Status.Ready = true
	return reconcile.Result{}, r.Status().Update(ctx, widget)
}

// --- Suite setup ---

var (
	testEnv    *envtest.Environment
	k8sClient  client.Client
	testCtx    context.Context
	testCancel context.CancelFunc
)

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	return s
}

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	binDir := os.Getenv("KUBEBUILDER_ASSETS")
	if binDir == "" {
		panic("KUBEBUILDER_ASSETS must be set\n  setup-envtest use 1.37 -p path")
	}

	testEnv = &envtest.Environment{
		BinaryAssetsDirectory: binDir,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{"widget_crd.yaml"},
		},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	s := buildScheme()
	k8sClient, err = client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		panic(err)
	}

	testCtx, testCancel = context.WithCancel(context.Background())

	// Start a real Manager with our reconciler
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 s,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		panic(err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&Widget{}).
		Complete(&WidgetReconciler{Client: mgr.GetClient()}); err != nil {
		panic(err)
	}

	go func() {
		if err := mgr.Start(testCtx); err != nil {
			panic(err)
		}
	}()

	code := m.Run()

	testCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

// --- Tests ---

func TestWidgetReconciler_CreatesConfigMap(t *testing.T) {
	widget := &Widget{
		Name: "envtest-widget", Namespace: "default",
		Spec: WidgetSpec{Color: "red"},
	}
	if err := k8sClient.Create(testCtx, widget); err != nil {
		t.Fatalf("create widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, widget) })

	// Poll until the reconciler creates the child ConfigMap
	var cm corev1.ConfigMap
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		err := k8sClient.Get(testCtx,
			types.NamespacedName{Name: "envtest-widget-config", Namespace: "default"}, &cm)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if cm.Name == "" {
		t.Fatal("timed out waiting for ConfigMap to be created")
	}
	if cm.Data["color"] != "red" {
		t.Errorf("expected color=red, got %s", cm.Data["color"])
	}
}

func TestWidgetReconciler_SetsStatusReady(t *testing.T) {
	widget := &Widget{
		Name: "status-widget", Namespace: "default",
		Spec: WidgetSpec{Color: "blue"},
	}
	if err := k8sClient.Create(testCtx, widget); err != nil {
		t.Fatalf("create widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, widget) })

	// Poll until status.ready = true
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		updated := &Widget{}
		_ = k8sClient.Get(testCtx,
			types.NamespacedName{Name: "status-widget", Namespace: "default"}, updated)
		if updated.Status.Ready {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for status.ready=true")
}
