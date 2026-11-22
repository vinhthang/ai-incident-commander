package k8s

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestCheckWorkloadStatus_DetectsActiveRolloutAndRecovery(t *testing.T) {
	t.Run("active deployment rollout detected", func(t *testing.T) {
		tmpDir := t.TempDir()
		kubectlScript := `#!/bin/sh
cat <<'EOF'
{
  "metadata": {"name": "vietcalendar", "generation": 4},
  "spec": {"replicas": 1},
  "status": {
    "observedGeneration": 3,
    "replicas": 2,
    "updatedReplicas": 1,
    "readyReplicas": 0,
    "availableReplicas": 0,
    "conditions": [{"type": "Progressing", "status": "True", "reason": "ReplicaSetUpdated"}]
  }
}
EOF
`
		if err := os.WriteFile(filepath.Join(tmpDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		status := CheckWorkloadStatus(map[string]string{
			"alertname":          "DeploymentReplicasUnavailable",
			"exported_namespace": "apps",
			"deployment":         "vietcalendar",
		})
		if !status.InProgress {
			t.Fatalf("expected InProgress=true during active rollout, got: %+v", status)
		}
		if status.Recovered {
			t.Fatalf("expected Recovered=false during active rollout, got: %+v", status)
		}
	})

	t.Run("healthy post-soak deployment marked recovered", func(t *testing.T) {
		tmpDir := t.TempDir()
		kubectlScript := `#!/bin/sh
cat <<'EOF'
{
  "metadata": {"name": "vietcalendar", "generation": 4},
  "spec": {"replicas": 1},
  "status": {
    "observedGeneration": 4,
    "replicas": 1,
    "updatedReplicas": 1,
    "readyReplicas": 1,
    "availableReplicas": 1,
    "conditions": [{"type": "Available", "status": "True"}]
  }
}
EOF
`
		if err := os.WriteFile(filepath.Join(tmpDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		status := CheckWorkloadStatus(map[string]string{
			"alertname":          "DeploymentReplicasUnavailable",
			"exported_namespace": "apps",
			"deployment":         "vietcalendar",
		})
		if status.InProgress {
			t.Fatalf("expected InProgress=false when healthy, got: %+v", status)
		}
		if !status.Recovered {
			t.Fatalf("expected Recovered=true when all desired replicas ready/available, got: %+v", status)
		}
	})
}

func TestFetchDiagnosticBundle_IncludesServiceEndpointsAndHelmChart(t *testing.T) {
	binDir := t.TempDir()
	wsDir := t.TempDir()

	// Create fake git workspace with Helm chart
	chartDir := filepath.Join(wsDir, "charts", "vinhthang-fleet")
	tmplDir := filepath.Join(chartDir, "templates", "apps")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	valuesYAML := "vietcalendar:\n  enabled: true\n  port: 8087\n  resources:\n    limits:\n      memory: 256Mi\notherSvc:\n  enabled: true\n"
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "vietcalendar.yaml"), []byte("kind: Deployment\nmetadata:\n  name: vietcalendar\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Initialize git repo for git log check
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@vinhthang.dev"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "feat(vietcalendar): initial helm manifest"},
	} {
		cmd := exec.Command("git", append([]string{"-C", wsDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	kubectlScript := `#!/bin/sh
case "$*" in
  *"get svc,endpoints"*)
    echo "NAME                   TYPE        CLUSTER-IP    PORT(S)"
    echo "service/vietcalendar   NodePort    10.43.0.10    8087:30006/TCP"
    echo "endpoints/vietcalendar 10.42.1.5:8087"
    ;;
  *"get deploy,sts,rs,pods"*)
    echo "NAME                           READY"
    echo "deployment.apps/vietcalendar   0/1"
    ;;
  *"describe pod"*)
    echo "Name: vietcalendar-0"
    echo "State: Waiting (ImagePullBackOff)"
    ;;
  *"describe deployment"*)
    echo "Replicas: 1 desired | 0 available"
    ;;
  *"get events"*)
    echo "Warning FailedToPullImage"
    ;;
  *"-o json"*)
    echo '{"spec":{"template":{"spec":{"containers":[{"name":"vietcalendar","image":"ghcr.io/vinhthang/vietcalendar:0.1.0","resources":{"limits":{"memory":"256Mi"}}}]}}}}'
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
		t.Fatal(err)
	}
	helmScript := `#!/bin/sh
if [ "$1" = "lint" ]; then
  echo "1 chart(s) linted, 0 chart(s) failed"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "helm"), []byte(helmScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	labels := map[string]string{
		"alertname":          "DeploymentReplicasUnavailable",
		"exported_namespace": "apps",
		"deployment":         "vietcalendar",
	}

	bundle := FetchDiagnosticBundle(labels, wsDir)

	for _, want := range []string{
		"=== Services & Endpoints ===",
		"service/vietcalendar",
		"=== Namespace Workloads ===",
		"=== Helm Chart & Values Check ===",
		"vietcalendar.yaml",
		"port: 8087",
		"1 chart(s) linted, 0 chart(s) failed",
		"initial helm manifest",
	} {
		if !strings.Contains(bundle, want) {
			t.Errorf("expected diagnostic bundle to contain %q, got:\n%s", want, bundle)
		}
	}
}

func TestFetchDiagnosticBundle_ResolvesWorkloadPodCrashLogsLastStateAndValuesKey(t *testing.T) {
	binDir := t.TempDir()
	wsDir := t.TempDir()

	chartDir := filepath.Join(wsDir, "charts", "vinhthang-fleet")
	tmplDir := filepath.Join(chartDir, "templates", "operators")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	valuesYAML := "kube-state-metrics:\n  namespaceOverride: observability\n\nmongodbOperator:\n  enabled: true\n  resources:\n    limits:\n      cpu: 200m\n"
	if err := os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYAML), 0644); err != nil {
		t.Fatal(err)
	}
	opTmpl := "{{- if .Values.mongodbOperator.enabled }}\nkind: Deployment\nmetadata:\n  name: psmdb-operator\n{{- end }}\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "psmdb-operator.yaml"), []byte(opTmpl), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@vinhthang.dev"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "feat(operators): add psmdb-operator manifest"},
	} {
		cmd := exec.Command("git", append([]string{"-C", wsDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	kubectlScript := `#!/bin/sh
case "$*" in
  *"get deployment psmdb-operator -n operators -o json"*)
    echo '{"spec":{"selector":{"matchLabels":{"app.kubernetes.io/name":"psmdb-operator"}},"template":{"spec":{"containers":[{"name":"psmdb-operator","image":"percona/percona-server-mongodb-operator:1.23.0","resources":{"limits":{"cpu":"200m","memory":"256Mi"}}}]}}}}'
    ;;
  *"get pods -n operators -l app.kubernetes.io/name=psmdb-operator -o json"*)
    echo '{"items":[{"metadata":{"name":"healthy-pod"},"status":{"containerStatuses":[{"name":"psmdb-operator","ready":true,"restartCount":0}]}},{"metadata":{"name":"psmdb-operator-ff79d9fb9-qqwbl"},"status":{"containerStatuses":[{"name":"psmdb-operator","ready":false,"restartCount":13,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 5m0s restarting failed container"}},"lastState":{"terminated":{"exitCode":1,"reason":"Error","finishedAt":"2026-11-21T20:11:38Z","message":"leader election lost"}}}]}}]}'
    ;;
  *"get pod psmdb-operator-ff79d9fb9-qqwbl -n operators -o json"*)
    echo '{"metadata":{"name":"psmdb-operator-ff79d9fb9-qqwbl"},"status":{"containerStatuses":[{"name":"psmdb-operator","ready":false,"restartCount":13,"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 5m0s restarting failed container"}},"lastState":{"terminated":{"exitCode":1,"reason":"Error","finishedAt":"2026-11-21T20:11:38Z","message":"leader election lost"}}}]}}'
    ;;
  *"logs -n operators psmdb-operator-ff79d9fb9-qqwbl --previous --tail=50"*)
    echo "Failed to renew lease: context deadline exceeded"
    ;;
  *"logs -n operators psmdb-operator-ff79d9fb9-qqwbl --tail=50"*)
    echo "Attempting to acquire leader lease..."
    ;;
  *"describe pod psmdb-operator-ff79d9fb9-qqwbl"*)
    echo "Name: psmdb-operator-ff79d9fb9-qqwbl"
    ;;
  *"get svc,endpoints"*)
    echo "No resources found in operators namespace."
    ;;
  *"get deploy,sts,rs,pods"*)
    echo "pod/psmdb-operator-ff79d9fb9-qqwbl 0/1 CrashLoopBackOff 13"
    ;;
  *"get events"*)
    echo "7m Normal LeaderElection lease/08db0feb.percona.com"
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
		t.Fatal(err)
	}
	helmScript := `#!/bin/sh
if [ "$1" = "lint" ]; then
  echo "1 chart(s) linted, 0 chart(s) failed"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "helm"), []byte(helmScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	labels := map[string]string{
		"alertname":          "DeploymentReplicasUnavailable",
		"exported_namespace": "operators",
		"namespace":          "observability",
		"pod":                "fleet-kube-state-metrics-6b6cd74c86-fq8mj",
		"service":            "fleet-kube-state-metrics",
		"deployment":         "psmdb-operator",
	}

	bundle := FetchDiagnosticBundle(labels, wsDir)

	for _, want := range []string{
		"=== Container Termination & Restart Status ===",
		"psmdb-operator-ff79d9fb9-qqwbl",
		"restartCount=13",
		"exitCode=1",
		"reason=Error",
		"=== Current Container Logs ===",
		"Attempting to acquire leader lease...",
		"=== Previous Container Logs ===",
		"Failed to renew lease: context deadline exceeded",
		"mongodbOperator:",
		"cpu: 200m",
	} {
		if !strings.Contains(bundle, want) {
			t.Errorf("expected diagnostic bundle to contain %q, got:\n%s", want, bundle)
		}
	}

	if helmIdx := strings.Index(bundle, "=== Helm Chart & Values Check ==="); helmIdx >= 0 {
		if strings.Contains(bundle[helmIdx:], "kube-state-metrics:") {
			t.Errorf("expected Helm values check to skip scraper label kube-state-metrics, got:\n%s", bundle[helmIdx:])
		}
	}
}
