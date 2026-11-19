package incident

import (
	"testing"
)

func TestComputeFingerprint(t *testing.T) {
	fp1 := ComputeFingerprint("KubePodCrashLooping", "monitoring", "loki-0")
	fp2 := ComputeFingerprint("KubePodCrashLooping", "monitoring", "loki-0")

	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if fp1 != fp2 {
		t.Fatalf("expected deterministic fingerprint, got %s and %s", fp1, fp2)
	}

	fpDifferentAlert := ComputeFingerprint("OtherAlert", "monitoring", "loki-0")
	if fp1 == fpDifferentAlert {
		t.Fatalf("expected different fingerprint for different alert name")
	}

	fpDifferentNamespace := ComputeFingerprint("KubePodCrashLooping", "default", "loki-0")
	if fp1 == fpDifferentNamespace {
		t.Fatalf("expected different fingerprint for different namespace")
	}

	fpDifferentTarget := ComputeFingerprint("KubePodCrashLooping", "monitoring", "tempo-0")
	if fp1 == fpDifferentTarget {
		t.Fatalf("expected different fingerprint for different target")
	}
}

func TestExtractTargetResource(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "exported_pod takes priority over pod",
			labels: map[string]string{
				"exported_pod": "exp-pod-1",
				"pod":          "pod-1",
			},
			expected: "exp-pod-1",
		},
		{
			name: "pod takes top priority",
			labels: map[string]string{
				"pod":         "mypod-123",
				"statefulset": "myset",
				"deployment":  "mydeploy",
				"service":     "mysvc",
				"app":         "myapp",
				"instance":    "10.0.0.1",
			},
			expected: "mypod-123",
		},
		{
			name: "statefulset takes priority over deployment",
			labels: map[string]string{
				"statefulset": "myset",
				"deployment":  "mydeploy",
				"service":     "mysvc",
				"app":         "myapp",
				"instance":    "10.0.0.1",
			},
			expected: "myset",
		},
		{
			name: "deployment takes priority over service",
			labels: map[string]string{
				"deployment": "mydeploy",
				"service":    "mysvc",
				"app":        "myapp",
				"instance":   "10.0.0.1",
			},
			expected: "mydeploy",
		},
		{
			name: "service takes priority over app",
			labels: map[string]string{
				"service":  "mysvc",
				"app":      "myapp",
				"instance": "10.0.0.1",
			},
			expected: "mysvc",
		},
		{
			name: "app takes priority over instance",
			labels: map[string]string{
				"app":      "myapp",
				"instance": "10.0.0.1",
			},
			expected: "myapp",
		},
		{
			name: "instance when other keys absent",
			labels: map[string]string{
				"instance": "10.0.0.1",
			},
			expected: "10.0.0.1",
		},
		{
			name: "http_url prioritized when k8s workloads absent",
			labels: map[string]string{
				"http_url": "http://10.10.0.4:8088/anon/",
				"instance": "otel-collector",
			},
			expected: "10.10.0.4:8088/anon/",
		},
		{
			name: "endpoint prioritized when instance is probe source",
			labels: map[string]string{
				"endpoint": "https://collab.vinhthang.dev",
				"instance": "blackbox",
			},
			expected: "https://collab.vinhthang.dev",
		},
		{
			name: "host prioritized over node and instance for Caddy 5xx alerts",
			labels: map[string]string{
				"host":     "vault.vinhthang.dev",
				"instance": "10.0.0.235:2019",
				"code":     "502",
			},
			expected: "vault.vinhthang.dev",
		},
		{
			name: "node prioritized over generic instance when no k8s workload",
			labels: map[string]string{
				"node":     "gce10",
				"instance": "node-exporter",
			},
			expected: "gce10",
		},
		{
			name:     "cluster fallback when no known keys",
			labels:   map[string]string{"foo": "bar"},
			expected: "cluster",
		},
		{
			name:     "cluster fallback when empty labels",
			labels:   map[string]string{},
			expected: "cluster",
		},
		{
			name: "skips fleet-kube-state-metrics scraper pod and extracts deployment",
			labels: map[string]string{
				"pod":        "fleet-kube-state-metrics-6b6cd74c86-fq8mj",
				"service":    "fleet-kube-state-metrics",
				"deployment": "vietcalendar",
			},
			expected: "vietcalendar",
		},
		{
			name: "skips kube-state-metrics scraper pod and extracts statefulset",
			labels: map[string]string{
				"pod":         "kube-state-metrics-0",
				"service":     "kube-state-metrics",
				"statefulset": "postgres-master",
			},
			expected: "postgres-master",
		},
		{
			name: "skips victoriametrics scraper pod and extracts job_name",
			labels: map[string]string{
				"pod":      "victoriametrics-0",
				"job_name": "nightly-backup",
			},
			expected: "nightly-backup",
		},
		{
			name: "skips scraper pod and extracts persistentvolumeclaim",
			labels: map[string]string{
				"pod":                   "fleet-kube-state-metrics-abc",
				"persistentvolumeclaim": "data-pg-0",
			},
			expected: "data-pg-0",
		},
		{
			name: "skips scraper service and falls back to http_url",
			labels: map[string]string{
				"pod":      "fleet-kube-state-metrics-abc",
				"service":  "fleet-kube-state-metrics",
				"http_url": "https://vietcalendar.vinhthang.dev/healthz",
			},
			expected: "vietcalendar.vinhthang.dev/healthz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := ExtractTargetResource(tc.labels)
			if res != tc.expected {
				t.Errorf("ExtractTargetResource() = %s, expected %s", res, tc.expected)
			}
		})
	}
}

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name: "exported_namespace takes priority over namespace",
			labels: map[string]string{
				"exported_namespace": "apps",
				"namespace":          "observability",
			},
			expected: "apps",
		},
		{
			name: "falls back to namespace when exported_namespace is absent",
			labels: map[string]string{
				"namespace": "data",
			},
			expected: "data",
		},
		{
			name:     "returns empty string when both absent",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := ExtractNamespace(tc.labels)
			if res != tc.expected {
				t.Errorf("ExtractNamespace() = %s, expected %s", res, tc.expected)
			}
		})
	}
}

func TestIsEphemeralNodeOutage(t *testing.T) {
	tests := []struct {
		name      string
		labels    map[string]string
		telemetry string
		expected  bool
	}{
		{
			name:      "k3s-worker-1 with NodeNotReady",
			labels:    map[string]string{"node": "k3s-worker-1"},
			telemetry: "Events: Node k3s-worker-1 status is NodeNotReady",
			expected:  true,
		},
		{
			name:      "k3s-worker-2 with flannel missing subnet",
			labels:    map[string]string{"node": "k3s-worker-2"},
			telemetry: "flannel subnet.env missing: failed to setup network",
			expected:  true,
		},
		{
			name:      "k3s-worker-3 with FailedCreatePodSandBox",
			labels:    map[string]string{"node": "k3s-worker-3"},
			telemetry: "FailedCreatePodSandBox: network plugin not ready",
			expected:  true,
		},
		{
			name:      "k3s-worker-4 with NetworkNotReady",
			labels:    map[string]string{"node": "k3s-worker-4"},
			telemetry: "NetworkNotReady: cni plugin not initialized",
			expected:  true,
		},
		{
			name:      "k3s-worker-1 with non-hardware error (CrashLoop)",
			labels:    map[string]string{"node": "k3s-worker-1"},
			telemetry: "Application panic: exit code 1",
			expected:  false,
		},
		{
			name:      "arm10 master node with NodeNotReady (not ephemeral worker)",
			labels:    map[string]string{"node": "arm10"},
			telemetry: "NodeNotReady",
			expected:  false,
		},
		{
			name:      "missing node label",
			labels:    map[string]string{},
			telemetry: "NodeNotReady",
			expected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsEphemeralNodeOutage(tc.labels, tc.telemetry)
			if got != tc.expected {
				t.Errorf("IsEphemeralNodeOutage() = %v, expected %v", got, tc.expected)
			}
		})
	}
}

func TestDatabaseNilSafety(t *testing.T) {
	// When db.DB is nil, functions must safely return without panicking
	isDup, issueNum, err := TrackClusterIncident("fakefp", "FakeAlert", "default", "pod-1")
	if err != nil || isDup || issueNum != 0 {
		t.Errorf("unexpected result with nil DB: %v, %v, %v", isDup, issueNum, err)
	}

	err = RecordClusterIssue("fakefp", "FakeAlert", "default", "pod-1", 123, "firing")
	if err != nil {
		t.Errorf("unexpected error with nil DB: %v", err)
	}

	closedIssue, err := ResolveClusterIncident("fakefp")
	if err != nil || closedIssue != 0 {
		t.Errorf("unexpected result with nil DB: %v, %v", closedIssue, err)
	}
}
