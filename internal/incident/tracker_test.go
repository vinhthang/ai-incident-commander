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
			name:     "cluster fallback when no known keys",
			labels:   map[string]string{"foo": "bar"},
			expected: "cluster",
		},
		{
			name:     "cluster fallback when empty labels",
			labels:   map[string]string{},
			expected: "cluster",
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
