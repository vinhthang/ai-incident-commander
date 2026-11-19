package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectDailyFleetSnapshot_FlagsUnhealthyPodsAndEmptyEndpoints(t *testing.T) {
	binDir := t.TempDir()
	wsDir := t.TempDir()

	chartDir := filepath.Join(wsDir, "charts", "vinhthang-fleet")
	if err := os.MkdirAll(chartDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("name: vinhthang-fleet\nversion: 1.0.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@vinhthang.dev"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-m", "chore: test baseline"},
	} {
		cmd := exec.Command("git", append([]string{"-C", wsDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	kubectlScript := `#!/bin/sh
case "$*" in
  *"get nodes"*)
    echo '{"items":[{"metadata":{"name":"arm10"},"status":{"conditions":[{"type":"Ready","status":"True"},{"type":"MemoryPressure","status":"False"}]}},{"metadata":{"name":"amd11"},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}'
    ;;
  *"get pods -A"*)
    echo '{"items":[{"metadata":{"namespace":"apps","name":"vietcalendar-ok"},"status":{"phase":"Running","containerStatuses":[{"name":"vietcalendar","ready":true,"restartCount":0}]}},{"metadata":{"namespace":"apps","name":"broken-app-0"},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}}]}'
    ;;
  *"get deployments,statefulsets -A"*)
    echo '{"items":[{"kind":"Deployment","metadata":{"namespace":"apps","name":"broken-app"},"spec":{"replicas":1},"status":{"readyReplicas":0,"availableReplicas":0}}]}'
    ;;
  *"get svc -A"*)
    echo '{"items":[{"metadata":{"namespace":"apps","name":"broken-svc"},"spec":{"type":"ClusterIP"}}]}'
    ;;
  *"get endpoints -A"*)
    echo '{"items":[{"metadata":{"namespace":"apps","name":"broken-svc"},"subsets":[]}]}'
    ;;
  *"get jobs"*)
    echo '{"items":[]}'
    ;;
  *"get pvc"*)
    echo '{"items":[]}'
    ;;
  *"get events -A"*)
    echo "apps   3m   Warning   BackOff   pod/broken-app-0   Back-off restarting failed container"
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
		t.Fatal(err)
	}
	helmScript := `#!/bin/sh
echo "1 chart(s) linted, 0 chart(s) failed"
`
	if err := os.WriteFile(filepath.Join(binDir, "helm"), []byte(helmScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	vmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "node_memory") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"node":"amd11"},"value":[1700000000,"82.5"]}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"http_url":"https://vinhthang.dev"},"value":[1700000000,"1"]}]}}`))
	}))
	defer vmServer.Close()

	snapshot := CollectDailyFleetSnapshot(context.Background(), wsDir, vmServer.URL)

	for _, want := range []string{
		"arm10",
		"amd11",
		"broken-app-0",
		"CrashLoopBackOff",
		"broken-svc",
		"0 ready endpoints",
		"1 chart(s) linted, 0 chart(s) failed",
		"82.5%",
		"https://vinhthang.dev",
	} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("expected daily snapshot to contain %q, got:\n%s", want, snapshot)
		}
	}
}
