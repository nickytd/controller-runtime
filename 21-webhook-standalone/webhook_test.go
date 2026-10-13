// SPDX-FileCopyrightText: 2026 nickytd
// SPDX-License-Identifier: Apache-2.0

// Case 21: Standalone webhook (no Manager) — handler-level test
//
// This sample builds an admission handler with admission.WithDefaulter and no
// Manager or cluster. envtest (used by cases 18–20) needs a Manager + apiserver,
// so it is the wrong tool here. The natural test drives the handler directly: we
// POST an AdmissionReview to the http.Handler and assert the response carries a
// JSON patch that defaults the Widget's color — exactly what the API server
// would receive in production.
//
// No KUBEBUILDER_ASSETS or cluster required: `go test ./...`.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// TestMain installs a logger before any test runs. The standalone defaulter
// logs via ctrl.Log, which panics if used before SetLogger — the envtest cases
// set it in their own TestMain; this handler-only package must too.
func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	os.Exit(m.Run())
}

// postReview sends an AdmissionReview carrying rawObject to the handler and
// returns the decoded response.
func postReview(t *testing.T, h http.Handler, rawObject []byte) *admissionv1.AdmissionResponse {
	t.Helper()
	review := admissionv1.AdmissionReview{
		APIVersion: "admission.k8s.io/v1",
		Kind:       "AdmissionReview",
		Request: &admissionv1.AdmissionRequest{
			UID: "test-uid",
			Kind: metav1.GroupVersionKind{
				Group: "example.com", Version: "v1alpha1", Kind: "Widget",
			},
			Resource: metav1.GroupVersionResource{
				Group: "example.com", Version: "v1alpha1", Resource: "widgets",
			},
			Operation: admissionv1.Create,
			Object:    runtime.RawExtension{Raw: rawObject},
		},
	}
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/mutate-widget", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	out := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("unmarshal response (%s): %v", rec.Body.String(), err)
	}
	if out.Response == nil {
		t.Fatal("response had no AdmissionResponse")
	}
	return out.Response
}

func newHandler() http.Handler {
	s := runtime.NewScheme()
	utilruntime.Must(SchemeBuilder.AddToScheme(s))
	return admission.WithCustomDefaulter(s, &Widget{}, &standaloneDefaulter{})
}

// TestStandaloneDefaulter_PatchesEmptyColor posts a Widget with no color and
// asserts the handler allows it and returns a patch setting color=green.
func TestStandaloneDefaulter_PatchesEmptyColor(t *testing.T) {
	widget := []byte(`{
		"apiVersion": "example.com/v1alpha1",
		"kind": "Widget",
		"metadata": {"name": "no-color"},
		"spec": {}
	}`)

	resp := postReview(t, newHandler(), widget)

	if !resp.Allowed {
		t.Fatalf("expected request to be allowed, got: %+v", resp.Result)
	}
	if resp.Patch == nil {
		t.Fatal("expected a JSON patch defaulting the color, got none")
	}

	// The patch is RFC 6902 JSON Patch. Apply-check it structurally: it must add
	// or replace /spec/color with "green".
	var ops []struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(resp.Patch, &ops); err != nil {
		t.Fatalf("unmarshal patch (%s): %v", resp.Patch, err)
	}
	found := false
	for _, op := range ops {
		if op.Path == "/spec/color" && op.Value == "green" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a patch op setting /spec/color=green, got: %s", resp.Patch)
	}
}

// TestStandaloneDefaulter_KeepsExplicitColor posts a Widget that already has a
// color and asserts the handler produces no color-changing patch.
func TestStandaloneDefaulter_KeepsExplicitColor(t *testing.T) {
	widget := []byte(`{
		"apiVersion": "example.com/v1alpha1",
		"kind": "Widget",
		"metadata": {"name": "has-color"},
		"spec": {"color": "red"}
	}`)

	resp := postReview(t, newHandler(), widget)

	if !resp.Allowed {
		t.Fatalf("expected request to be allowed, got: %+v", resp.Result)
	}
	// With color already set, the defaulter is a no-op. The framework may emit
	// an empty patch or none; either way, it must not rewrite /spec/color.
	if resp.Patch != nil {
		var ops []struct {
			Path  string `json:"path"`
			Value any    `json:"value"`
		}
		if err := json.Unmarshal(resp.Patch, &ops); err != nil {
			t.Fatalf("unmarshal patch (%s): %v", resp.Patch, err)
		}
		for _, op := range ops {
			if op.Path == "/spec/color" {
				t.Errorf("expected no change to /spec/color, got op setting it to %v", op.Value)
			}
		}
	}
}
