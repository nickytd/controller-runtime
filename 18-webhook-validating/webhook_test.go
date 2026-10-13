// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 18: Validating admission webhook — envtest integration test
//
// envtest installs the CRD and the ValidatingWebhookConfiguration, generates
// serving certs, and rewrites the webhook clientConfig (url + caBundle) to point
// at the manager's local webhook server.
// That lets us assert end-to-end that a bad Widget is rejected and a
// good one is accepted.
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
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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
		// envtest installs this ValidatingWebhookConfiguration and rewrites its
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
	// allocated and wired into the ValidatingWebhookConfiguration above.
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

	if err := ctrl.NewWebhookManagedBy[*Widget](mgr, &Widget{}).
		WithValidator(&WidgetValidator{}).
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

func newWidget(name, color string, size int) *Widget {
	w := &Widget{Spec: WidgetSpec{Color: color, Size: size}}
	w.APIVersion = GroupVersion.String()
	w.Kind = "Widget"
	w.Name = name
	w.Namespace = "default"
	return w
}

func TestWebhook_RejectsInvalidWidget(t *testing.T) {
	// color=purple (not in {red,green,blue}) and size=0 (outside 1..10) — the
	// webhook should reject with both field errors, and nothing is persisted.
	err := k8sClient.Create(testCtx, newWidget("bad-widget", "purple", 0))
	if err == nil {
		t.Fatal("expected creation to be rejected by the validating webhook, but it was accepted")
	}
	// A webhook denial surfaces as a Forbidden-status API error whose message
	// carries the aggregated field errors (not an Invalid/422 — that status is
	// for apiserver schema validation, not admission).
	if !apierrors.IsForbidden(err) {
		t.Fatalf("expected a Forbidden admission denial, got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"denied the request", "spec.color", "spec.size"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected rejection message to contain %q; got: %s", want, msg)
		}
	}
}

func TestWebhook_AcceptsValidWidget(t *testing.T) {
	good := newWidget("good-widget", "red", 5)
	if err := k8sClient.Create(testCtx, good); err != nil {
		t.Fatalf("expected valid widget to be accepted, got: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(testCtx, good) })
}
