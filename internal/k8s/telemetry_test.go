package k8s

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchTelemetry_SyntheticEndpoint_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Caddy")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>directory index</html>"))
	}))
	defer server.Close()

	labels := map[string]string{
		"alertname": "EndpointDown",
		"http_url":  server.URL + "/anon/",
	}

	result := FetchTelemetry(labels)

	if !strings.Contains(result, "=== Active HTTP/TLS Probe ===") {
		t.Fatalf("expected Active HTTP/TLS Probe in telemetry, got: %s", result)
	}
	if !strings.Contains(result, "HTTP 200") {
		t.Fatalf("expected HTTP 200 in telemetry probe, got: %s", result)
	}
	if !strings.Contains(result, "Server: Caddy") {
		t.Fatalf("expected Server: Caddy header in telemetry probe, got: %s", result)
	}
}

func TestFetchTelemetry_SyntheticEndpoint_ConnectionRefused(t *testing.T) {
	// A closed port on localhost guaranteed to refuse connection
	labels := map[string]string{
		"alertname": "EndpointDown",
		"http_url":  "http://127.0.0.1:59999/anon/",
		"node":      "gce10",
	}

	result := FetchTelemetry(labels)

	if !strings.Contains(result, "=== Active HTTP/TLS Probe ===") {
		t.Fatalf("expected Active HTTP/TLS Probe in telemetry, got: %s", result)
	}
	if !strings.Contains(result, "Probe Status: FAILED") {
		t.Fatalf("expected Probe Status: FAILED in telemetry, got: %s", result)
	}
	if !strings.Contains(result, "connection refused") && !strings.Contains(result, "connect") {
		t.Fatalf("expected connection error in telemetry, got: %s", result)
	}
	if !strings.Contains(result, "Edge Node: gce10") {
		t.Fatalf("expected Edge Node: gce10 in telemetry, got: %s", result)
	}
}

func TestFetchTelemetry_EmptyLabels(t *testing.T) {
	labels := map[string]string{}
	result := FetchTelemetry(labels)
	if !strings.Contains(result, "No namespace or pod specified") {
		t.Fatalf("expected fallback message on empty labels, got: %s", result)
	}
}

func TestFetchTelemetry_CaddyHostLabel_ActiveTLSProbe(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Caddy")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Bad Gateway"))
	}))
	defer server.Close()

	labels := map[string]string{
		"alertname": "Ingress5xxError",
		"host":      strings.TrimPrefix(server.URL, "https://"),
		"instance":  "10.0.0.235:2019",
		"code":      "502",
	}

	result := FetchTelemetry(labels)

	if !strings.Contains(result, "=== Active HTTP/TLS Probe ===") {
		t.Fatalf("expected Active HTTP/TLS Probe in telemetry, got: %s", result)
	}
	if !strings.Contains(result, "HTTP 502") {
		t.Fatalf("expected HTTP 502 in telemetry probe, got: %s", result)
	}
	if !strings.Contains(result, "Server: Caddy") {
		t.Fatalf("expected Server: Caddy in telemetry probe, got: %s", result)
	}
}

func TestFetchTelemetry_SkipsScraperPodAndUsesWorkload(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "kubectl_calls.log")
	t.Setenv("KUBECTL_LOG_FILE", logFile)

	kubectlScript := `#!/bin/sh
echo "$*" >> "$KUBECTL_LOG_FILE"
case "$1 $2" in
  "describe deployment")
    echo "Name: vietcalendar"
    echo "Replicas: 1 desired | 0 available | 1 unavailable"
    ;;
  "get pods")
    echo "NAME                           READY   STATUS             RESTARTS   AGE"
    echo "vietcalendar-79d86cc9c-abcde   0/1     ImagePullBackOff   0          5m"
    ;;
  "get events")
    echo "5m   Warning   FailedScheduling   pod/vietcalendar-79d86cc9c-abcde   Failed to pull image"
    ;;
esac
`
	kubectlPath := filepath.Join(tmpDir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(kubectlScript), 0755); err != nil {
		t.Fatalf("failed to write fake kubectl: %v", err)
	}
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	labels := map[string]string{
		"alertname":          "DeploymentReplicasUnavailable",
		"category":           "workload",
		"exported_namespace": "apps",
		"namespace":          "observability",
		"pod":                "fleet-kube-state-metrics-6b6cd74c86-fq8mj",
		"service":            "fleet-kube-state-metrics",
		"deployment":         "vietcalendar",
	}

	result := FetchTelemetry(labels)

	callsBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected kubectl to be invoked, err=%v", err)
	}
	calls := string(callsBytes)

	if strings.Contains(calls, "fleet-kube-state-metrics") {
		t.Fatalf("expected FetchTelemetry NOT to query scraper pod fleet-kube-state-metrics, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "describe deployment vietcalendar -n apps") {
		t.Fatalf("expected kubectl describe deployment vietcalendar -n apps, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "get pods -n apps -l app=vietcalendar") {
		t.Fatalf("expected kubectl get pods -n apps -l app=vietcalendar, calls:\n%s", calls)
	}
	if !strings.Contains(calls, "get events -n apps --sort-by=.lastTimestamp") {
		t.Fatalf("expected kubectl get events -n apps --sort-by=.lastTimestamp, calls:\n%s", calls)
	}
	if !strings.Contains(result, "Replicas: 1 desired | 0 available") {
		t.Fatalf("expected deployment describe output in telemetry, got:\n%s", result)
	}
	if !strings.Contains(result, "ImagePullBackOff") {
		t.Fatalf("expected pod status output in telemetry, got:\n%s", result)
	}
}


