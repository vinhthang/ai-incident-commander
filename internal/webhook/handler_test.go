package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/state"
)

func TestComputeAlertIdentity_DistinctTargetsDoNotCollideOnLock(t *testing.T) {
	state.StateMu.Lock()
	state.ActiveIncidents = make(map[string]*state.IncidentState)
	config.CooldownMins = 5 * time.Minute
	state.StateMu.Unlock()

	// Two EndpointDown alerts for different http_urls
	ns1, tgt1, fp1 := computeAlertIdentity("EndpointDown", map[string]string{
		"alertname": "EndpointDown",
		"http_url":  "https://vault.vinhthang.dev/alive",
	})
	ns2, tgt2, fp2 := computeAlertIdentity("EndpointDown", map[string]string{
		"alertname": "EndpointDown",
		"http_url":  "http://10.10.0.4:8088/anon/",
	})
	if fp1 == fp2 {
		t.Fatalf("expected distinct fingerprints for distinct http_url targets (%s/%s vs %s/%s), got identical %s", ns1, tgt1, ns2, tgt2, fp1)
	}
	if !state.AcquireIncidentLock(fp1, "firing") {
		t.Fatalf("expected lock acquisition for first EndpointDown target")
	}
	if !state.AcquireIncidentLock(fp2, "firing") {
		t.Fatalf("expected concurrent lock acquisition for second EndpointDown target")
	}

	// Two Ingress5xxError alerts for different hosts
	_, host1, fpHost1 := computeAlertIdentity("Ingress5xxError", map[string]string{
		"alertname": "Ingress5xxError",
		"host":      "vault.vinhthang.dev",
	})
	_, host2, fpHost2 := computeAlertIdentity("Ingress5xxError", map[string]string{
		"alertname": "Ingress5xxError",
		"host":      "collab.vinhthang.dev",
	})
	if fpHost1 == fpHost2 || host1 == host2 {
		t.Fatalf("expected distinct fingerprints and targets for distinct hosts, got %s (%s) vs %s (%s)", host1, fpHost1, host2, fpHost2)
	}
	if !state.AcquireIncidentLock(fpHost1, "firing") {
		t.Fatalf("expected lock acquisition for first host")
	}
	if !state.AcquireIncidentLock(fpHost2, "firing") {
		t.Fatalf("expected concurrent lock acquisition for second host")
	}

	// Two PodCrashLoopBackOff alerts with exported_pod and exported_namespace
	expNs1, expPod1, fpPod1 := computeAlertIdentity("PodCrashLoopBackOff", map[string]string{
		"alertname":          "PodCrashLoopBackOff",
		"exported_pod":       "pod-alpha",
		"exported_namespace": "ns-alpha",
		"namespace":          "observability",
		"pod":                "kube-state-metrics-0",
	})
	expNs2, expPod2, fpPod2 := computeAlertIdentity("PodCrashLoopBackOff", map[string]string{
		"alertname":          "PodCrashLoopBackOff",
		"exported_pod":       "pod-beta",
		"exported_namespace": "ns-beta",
		"namespace":          "observability",
		"pod":                "kube-state-metrics-0",
	})
	if expNs1 != "ns-alpha" || expNs2 != "ns-beta" {
		t.Fatalf("expected exported_namespace to be extracted, got %s and %s", expNs1, expNs2)
	}
	if expPod1 != "pod-alpha" || expPod2 != "pod-beta" {
		t.Fatalf("expected exported_pod to be extracted, got %s and %s", expPod1, expPod2)
	}
	if fpPod1 == fpPod2 {
		t.Fatalf("expected distinct fingerprints for distinct exported_pod targets, got %s", fpPod1)
	}
}

func TestIsApplicationErrorAlert(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		annotations map[string]string
		expected    bool
	}{
		{
			name: "DeploymentReplicasUnavailable in apps namespace is infrastructure alert",
			labels: map[string]string{
				"alertname":          "DeploymentReplicasUnavailable",
				"category":           "workload",
				"exported_namespace": "apps",
				"deployment":         "vietcalendar",
			},
			expected: false,
		},
		{
			name: "PodOOMKilled in apps namespace is infrastructure alert",
			labels: map[string]string{
				"alertname":          "PodOOMKilled",
				"category":           "workload",
				"exported_namespace": "apps",
				"exported_pod":       "vietcalendar-0",
			},
			expected: false,
		},
		{
			name: "explicit application category routes to AppFixer",
			labels: map[string]string{
				"alertname": "Service5xxSpike",
				"category":  "application",
				"namespace": "apps",
			},
			expected: true,
		},
		{
			name: "trace_id label routes to AppFixer",
			labels: map[string]string{
				"alertname": "UnhandledPanic",
				"namespace": "apps",
				"trace_id":  "4bf92f3577b34da6a3ce929d0e0e4736",
			},
			expected: true,
		},
		{
			name: "status_code label routes to AppFixer",
			labels: map[string]string{
				"alertname":   "HTTP500Spike",
				"namespace":   "apps",
				"status_code": "500",
			},
			expected: true,
		},
		{
			name: "Pulsar topic label routes to AppFixer",
			labels: map[string]string{
				"alertname": "ConsumerLag",
				"namespace": "apps",
				"topic":     "persistent://public/default/orders",
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isApplicationErrorAlert(tc.labels, tc.annotations)
			if got != tc.expected {
				t.Errorf("isApplicationErrorAlert() = %v, expected %v", got, tc.expected)
			}
		})
	}
}

func TestHandleWebhook_BearerAuth(t *testing.T) {
	origToken := config.WebhookAuthToken
	defer func() { config.WebhookAuthToken = origToken }()

	payload := `{"status":"firing","alerts":[]}`

	// Case 1: Token configured -> missing header rejected with 401
	config.WebhookAuthToken = "super-secret-token"
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	rr := httptest.NewRecorder()
	HandleWebhook(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when missing Authorization header, got %d", rr.Code)
	}

	// Case 2: Token configured -> wrong bearer token rejected with 401
	reqBad := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	reqBad.Header.Set("Authorization", "Bearer wrong-token")
	rrBad := httptest.NewRecorder()
	HandleWebhook(rrBad, reqBad)
	if rrBad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong Bearer token, got %d", rrBad.Code)
	}

	// Case 3: Token configured -> matching bearer token succeeds with 200
	reqOK := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	reqOK.Header.Set("Authorization", "Bearer super-secret-token")
	rrOK := httptest.NewRecorder()
	HandleWebhook(rrOK, reqOK)
	if rrOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid Bearer token, got %d: %s", rrOK.Code, rrOK.Body.String())
	}

	// Case 4: Token empty -> backwards-compatible open webhook in local dev
	config.WebhookAuthToken = ""
	reqOpen := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(payload))
	rrOpen := httptest.NewRecorder()
	HandleWebhook(rrOpen, reqOpen)
	if rrOpen.Code != http.StatusOK {
		t.Fatalf("expected 200 when WEBHOOK_AUTH_TOKEN is unset, got %d", rrOpen.Code)
	}
}

func TestHandleHealthz(t *testing.T) {
	reqMethod := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rrMethod := httptest.NewRecorder()
	HandleHealthz(rrMethod, reqMethod)
	if rrMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on POST /healthz, got %d", rrMethod.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	HandleHealthz(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /healthz with nil DB, got %d: %s", rr.Code, rr.Body.String())
	}

	var body struct {
		Status     string `json:"status"`
		DB         string `json:"db"`
		QueueDepth int    `json:"queue_depth"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode /healthz JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status=ok, got %q", body.Status)
	}
	if body.DB != "disabled" {
		t.Errorf("expected db=disabled when db.DB is nil, got %q", body.DB)
	}
	if body.QueueDepth < 0 {
		t.Errorf("expected non-negative queue_depth, got %d", body.QueueDepth)
	}
}
