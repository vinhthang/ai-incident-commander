package webhook

import (
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
