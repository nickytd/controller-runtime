// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 20: Conversion webhook (multi-version CRD) — envtest integration test
//
// Why this test exists: a conversion webhook only runs if the CRD declares
// `spec.conversion.strategy: Webhook` and the API server can reach the webhook.
// Running `go run` alone leaves the conversion untested — and on kind/minikube
// the apiserver can't reach a host process at all. The result is that the
// hub<->spoke round-trip (size <-> magnitude) is never actually exercised.
//
// envtest closes that gap: given a scheme whose types are convertible (Hub +
// Spoke), it rewrites the CRD's conversion clientConfig to the manager's local
// webhook server and injects the caBundle. That lets us create a Widget in one
// version and read it back in the other, asserting the field rename round-trips.
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

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilderV1.AddToScheme(s))
	utilruntime.Must(SchemeBuilderV1Beta.AddToScheme(s))
	return s
}

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		panic("KUBEBUILDER_ASSETS must be set\n  setup-envtest use 1.37 -p path")
	}

	s := buildScheme()

	testEnv = &envtest.Environment{
		BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
		// Scheme lets envtest detect that Widget is convertible and rewrite the
		// CRD's conversion webhook clientConfig to the local serving endpoint.
		Scheme: s,
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths: []string{"widget_crd.yaml"},
		},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		panic(err)
	}

	testCtx, testCancel = context.WithCancel(context.Background())

	// Point the manager's webhook server at the host/port/cert dir envtest
	// allocated and wired into the CRD's conversion webhook config.
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

	// Registering the spoke type auto-registers the /convert handler because
	// Widget (hub) and WidgetV1Beta1 (spoke) satisfy the conversion interfaces.
	if err := ctrl.NewWebhookManagedBy(mgr, &WidgetV1Beta1{}).
		Complete(); err != nil {
		panic(err)
	}

	go func() {
		if err := mgr.Start(testCtx); err != nil {
			panic(err)
		}
	}()

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

// TestConversion_SpokeToHub creates a Widget via the v1beta1 spoke (magnitude)
// and reads it back as the v1alpha1 hub, asserting magnitude -> size.
func TestConversion_SpokeToHub(t *testing.T) {
	beta := &WidgetV1Beta1{}
	beta.APIVersion = GVv1beta1.String()
	beta.Kind = "Widget"
	beta.Name = "beta-created"
	beta.Namespace = "default"
	beta.Spec.Color = "green"
	beta.Spec.Magnitude = 7
	if err := k8sClient.Create(testCtx, beta); err != nil {
		t.Fatalf("create v1beta1 widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, beta) })

	hub := &Widget{}
	if err := k8sClient.Get(testCtx,
		types.NamespacedName{Name: "beta-created", Namespace: "default"}, hub); err != nil {
		t.Fatalf("get as v1alpha1 hub: %v", err)
	}
	if hub.Spec.Color != "green" {
		t.Errorf("expected color green, got %q", hub.Spec.Color)
	}
	if hub.Spec.Size != 7 {
		t.Errorf("expected magnitude 7 to convert to size 7, got %d", hub.Spec.Size)
	}
}

// TestConversion_HubToSpoke creates a Widget via the v1alpha1 hub (size) and
// reads it back as the v1beta1 spoke, asserting size -> magnitude.
func TestConversion_HubToSpoke(t *testing.T) {
	hub := &Widget{}
	hub.APIVersion = GVv1alpha1.String()
	hub.Kind = "Widget"
	hub.Name = "hub-created"
	hub.Namespace = "default"
	hub.Spec.Color = "red"
	hub.Spec.Size = 3
	if err := k8sClient.Create(testCtx, hub); err != nil {
		t.Fatalf("create v1alpha1 widget: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, hub) })

	beta := &WidgetV1Beta1{}
	if err := k8sClient.Get(testCtx,
		types.NamespacedName{Name: "hub-created", Namespace: "default"}, beta); err != nil {
		t.Fatalf("get as v1beta1 spoke: %v", err)
	}
	if beta.Spec.Color != "red" {
		t.Errorf("expected color red, got %q", beta.Spec.Color)
	}
	if beta.Spec.Magnitude != 3 {
		t.Errorf("expected size 3 to convert to magnitude 3, got %d", beta.Spec.Magnitude)
	}
}
