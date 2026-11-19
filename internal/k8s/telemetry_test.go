package k8s

import (
	"net/http"
	"net/http/httptest"
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

