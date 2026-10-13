// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 19: Mutating admission webhook (defaulter) — envtest integration test
//
// Why this test exists: a mutating webhook only runs if the API server is told
// to call it via a MutatingWebhookConfiguration. Running the server with
// `go run` alone leaves the webhook unregistered — on a kind/minikube cluster
// the apiserver can't even reach a webhook process on your host — so the
// defaulting silently never happens and a Widget is stored with empty fields.
//
// envtest closes that gap: it stands up a real apiserver, installs the CRD and
// the MutatingWebhookConfiguration, generates serving certs, and rewrites the
// webhook clientConfig to point at the manager's local webhook server. That lets
// us assert end-to-end that an unset Widget comes back defaulted.
//
// Run with:
//
//	export KUBEBUILDER_ASSETS=$(setup-envtest use 1.37 -p path)
//	go test ./... -v
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var (
	testEnv    *envtest.Environment
	k8sClient  client.Client
	testCtx    context.Context
	testCancel context.CancelFunc
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		panic("KUBEBUILDER_ASSETS must be set\n  setup-envtest use 1.37 -p path")
	}

	testEnv = &envtest.Environment{
		BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{"widget_crd.yaml"},
		},
		// envtest installs this MutatingWebhookConfiguration and rewrites its
		// clientConfig (url + caBundle) to point at the local webhook server.
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{"webhook_config.yaml"},
		},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))

	k8sClient, err = client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		panic(err)
	}

	testCtx, testCancel = context.WithCancel(context.Background())

	// Point the manager's webhook server at the host/port/cert dir envtest
	// allocated and wired into the MutatingWebhookConfiguration above.
	wo := testEnv.WebhookInstallOptions
	mgr, err := manager.New(cfg, manager.Options{
		Scheme:                 s,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    wo.LocalServingHost,
			Port:    wo.LocalServingPort,
			CertDir: wo.LocalServingCertDir,
		}),
	})
	if err != nil {
		panic(err)
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &Widget{}).
		WithDefaulter(&WidgetDefaulter{}).
		Complete(); err != nil {
		panic(err)
	}

	go func() {
		if err := mgr.Start(testCtx); err != nil {
			panic(err)
		}
	}()

	// Wait for the webhook server to accept connections before running tests;
	// otherwise the first request races the TLS listener coming up.
	if err := waitForWebhookReady(wo); err != nil {
		panic(err)
	}

	code := m.Run()

	testCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

// waitForWebhookReady polls the manager's webhook server until its TLS port
// accepts connections, so tests don't race the listener's startup.
func waitForWebhookReady(wo envtest.WebhookInstallOptions) error {
	addr := net.JoinHostPort(wo.LocalServingHost, strconv.Itoa(wo.LocalServingPort))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("webhook server at %s not ready within deadline", addr)
}

func newWidget(name string) *Widget {
	w := &Widget{}
	w.APIVersion = GroupVersion.String()
	w.Kind = "Widget"
	w.Name = name
	w.Namespace = "default"
	return w
}

// TestDefaulter_SetsMissingFields creates a Widget with no color/size and
// asserts the mutating webhook filled in the defaults before it was stored.
func TestDefaulter_SetsMissingFields(t *testing.T) {
	w := newWidget("empty-widget")
	if err := k8sClient.Create(testCtx, w); err != nil {
		t.Fatalf("create widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, w) })

	got := &Widget{}
	if err := k8sClient.Get(testCtx,
		types.NamespacedName{Name: "empty-widget", Namespace: "default"}, got); err != nil {
		t.Fatalf("get widget: %v", err)
	}

	if got.Spec.Color != "blue" {
		t.Errorf("expected defaulted color=blue, got %q", got.Spec.Color)
	}
	if got.Spec.Size != 5 {
		t.Errorf("expected defaulted size=5, got %d", got.Spec.Size)
	}
	if got.Labels["defaulted-by"] != "widget-defaulter" {
		t.Errorf("expected label defaulted-by=widget-defaulter, got %q", got.Labels["defaulted-by"])
	}
}

// TestDefaulter_KeepsExplicitValues confirms the defaulter only fills unset
// fields: an explicit color/size is preserved while the label is still added.
func TestDefaulter_KeepsExplicitValues(t *testing.T) {
	w := newWidget("set-widget")
	w.Spec.Color = "red"
	w.Spec.Size = 9
	if err := k8sClient.Create(testCtx, w); err != nil {
		t.Fatalf("create widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, w) })

	got := &Widget{}
	if err := k8sClient.Get(testCtx,
		types.NamespacedName{Name: "set-widget", Namespace: "default"}, got); err != nil {
		t.Fatalf("get widget: %v", err)
	}

	if got.Spec.Color != "red" {
		t.Errorf("expected color to stay red, got %q", got.Spec.Color)
	}
	if got.Spec.Size != 9 {
		t.Errorf("expected size to stay 9, got %d", got.Spec.Size)
	}
	if got.Labels["defaulted-by"] != "widget-defaulter" {
		t.Errorf("expected label defaulted-by=widget-defaulter, got %q", got.Labels["defaulted-by"])
	}
}
