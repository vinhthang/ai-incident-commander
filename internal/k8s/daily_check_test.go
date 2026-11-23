package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestCollectDailyFleetSnapshot_IgnoresSelfTriggerSupersededCronJobsAndOldRestarts(t *testing.T) {
	binDir := t.TempDir()
	now := time.Now().UTC()
	oldTS := now.Add(-480 * time.Hour).Format(time.RFC3339)
	recentTS := now.Add(-2 * time.Hour).Format(time.RFC3339)

	podsJSON := fmt.Sprintf(`{"items":[
		{"metadata":{"namespace":"system","name":"ai-commander-daily-report-29921775-rlzr5","labels":{"app":"ai-commander-daily-report","job-name":"ai-commander-daily-report-29921775"}},"status":{"phase":"Running","containerStatuses":[{"name":"trigger","ready":false,"restartCount":0}]}},
		{"metadata":{"namespace":"data","name":"postgres-primary-0"},"status":{"phase":"Running","containerStatuses":[{"name":"postgres","ready":true,"restartCount":1,"lastState":{"terminated":{"finishedAt":%q}},"state":{"running":{"startedAt":%q}}}]}},
		{"metadata":{"namespace":"apps","name":"recent-crash-0"},"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":true,"restartCount":2,"lastState":{"terminated":{"finishedAt":%q}},"state":{"running":{"startedAt":%q}}}]}}
	]}`, oldTS, oldTS, recentTS, recentTS)

	jobsJSON := `{"items":[
		{"metadata":{"namespace":"system","name":"ai-commander-daily-report-29918895","creationTimestamp":"2026-11-20T00:15:00Z","ownerReferences":[{"kind":"CronJob","name":"ai-commander-daily-report"}]},"status":{"failed":1}},
		{"metadata":{"namespace":"system","name":"ai-commander-daily-report-29921775","creationTimestamp":"2026-11-22T00:15:00Z","ownerReferences":[{"kind":"CronJob","name":"ai-commander-daily-report"}]},"status":{"failed":0}},
		{"metadata":{"namespace":"system","name":"broken-cron-100","creationTimestamp":"2026-11-21T01:00:00Z","ownerReferences":[{"kind":"CronJob","name":"broken-cron"}]},"status":{"failed":0}},
		{"metadata":{"namespace":"system","name":"broken-cron-200","creationTimestamp":"2026-11-22T01:00:00Z","ownerReferences":[{"kind":"CronJob","name":"broken-cron"}]},"status":{"failed":1}},
		{"metadata":{"namespace":"apps","name":"db-migrate","creationTimestamp":"2026-11-22T02:00:00Z"},"status":{"failed":1}}
	]}`

	kubectlScript := fmt.Sprintf(`#!/bin/sh
case "$*" in
  *"get nodes"*) echo '{"items":[]}' ;;
  *"get pods -A"*) cat <<'EOF'
%s
EOF
    ;;
  *"get deployments,statefulsets -A"*) echo '{"items":[]}' ;;
  *"get svc -A"*) echo '{"items":[]}' ;;
  *"get endpoints -A"*) echo '{"items":[]}' ;;
  *"get jobs"*) cat <<'EOF'
%s
EOF
    ;;
  *"get pvc"*) echo '{"items":[]}' ;;
  *"get events -A"*) echo "" ;;
esac
`, podsJSON, jobsJSON)

	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectlScript), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snapshot := CollectDailyFleetSnapshot(context.Background(), "", "")

	for _, forbidden := range []string{
		"ai-commander-daily-report-29921775-rlzr5",
		"postgres-primary-0",
		"ai-commander-daily-report-29918895",
	} {
		if strings.Contains(snapshot, forbidden) {
			t.Errorf("expected daily snapshot to ignore %q, got:\n%s", forbidden, snapshot)
		}
	}

	for _, want := range []string{
		"Healthy Running Pods: 1",
		"apps/recent-crash-0",
		"system/broken-cron-200",
		"apps/db-migrate",
	} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("expected daily snapshot to retain %q, got:\n%s", want, snapshot)
		}
	}
}
