package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v59/github"
	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/metrics"
)

func TestExecuteDailyReport_DispatchesProactiveFixesWithinCap(t *testing.T) {
	origClient := github.Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	origMemos := config.MemosDBDSN
	origEnable := config.EnableDailyProactiveFix
	origCap := config.DailyMaxProactiveFixes
	origSnap := dailySnapshotCollector
	origSum := dailyIncidentSummarizer
	origEval := dailyReportEvaluator
	origBundle := dailyDiagnosticBundleFn
	origPR := dailyPRRemediator
	origExec := runtimeActionExecutor
	origInsert := memosInsertFn
	defer func() {
		github.Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
		config.MemosDBDSN = origMemos
		config.EnableDailyProactiveFix = origEnable
		config.DailyMaxProactiveFixes = origCap
		dailySnapshotCollector = origSnap
		dailyIncidentSummarizer = origSum
		dailyReportEvaluator = origEval
		dailyDiagnosticBundleFn = origBundle
		dailyPRRemediator = origPR
		runtimeActionExecutor = origExec
		memosInsertFn = origInsert
		metrics.ResetForTest()
	}()

	metrics.ResetForTest()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"
	config.MemosDBDSN = "postgres://memos-test/db"
	config.EnableDailyProactiveFix = true
	config.DailyMaxProactiveFixes = 2

	var mu sync.Mutex
	nextIssue := 800
	createdTitles := []string{}
	dailyIssueBody := ""
	dailySummaryComment := ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/vinhthang/oci/issues":
			// Duplicate open issue exists for apps/already-open
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"number": 799, "title": "[Alert] Ingress5xxError: apps/already-open"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/vinhthang/oci/pulls":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues":
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			mu.Lock()
			nextIssue++
			num := nextIssue
			title, _ := p["title"].(string)
			createdTitles = append(createdTitles, title)
			if strings.Contains(title, "[Daily Fleet Check]") {
				dailyIssueBody, _ = p["body"].(string)
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"number": num})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			var p map[string]string
			_ = json.NewDecoder(r.Body).Decode(&p)
			mu.Lock()
			if strings.Contains(r.URL.Path, "/issues/801/comments") {
				dailySummaryComment = p["body"]
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/assignees"):
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 801})
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/issues/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 802, "state": "closed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	github.Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	github.Client.BaseURL = baseURL

	dailySnapshotCollector = func(ctx context.Context, workspaceDir, vmURL string) string {
		return "snapshot-data"
	}
	dailyIncidentSummarizer = func(window time.Duration) string {
		return "incident-summary"
	}
	dailyReportEvaluator = func(ctx context.Context, dateStr, snapshot, incSummary string) (string, bool, error) {
		report := strings.Join([]string{
			"## Daily Fleet Check — " + dateStr,
			"**Executive Status**: Attention needed on known root causes.",
			"",
			"PROACTIVE_ACTION: FIX_VIA_PR | apps/already-open | Duplicate open issue should be skipped",
			"PROACTIVE_ACTION: FIX_RUNTIME | ROLLOUT_RESTART observability/deployment/grafana | SQLite lock in grafana",
			"PROACTIVE_ACTION: FIX_VIA_PR | observability/kube-state-metrics | OOMKilled bump memory in values.yaml",
			"PROACTIVE_ACTION: FIX_VIA_PR | apps/vietcalendar | Should not execute past cap=2",
			"",
			"HEALTH_STATUS: ATTENTION_NEEDED",
		}, "\n")
		return report, false, nil
	}
	dailyDiagnosticBundleFn = func(labels map[string]string) string {
		return "bundle-telemetry"
	}

	var runtimeCalls atomic.Int32
	runtimeActionExecutor = func(ctx context.Context, act k8s.RuntimeAction) k8s.RuntimeActionResult {
		runtimeCalls.Add(1)
		return k8s.RuntimeActionResult{
			Action:       act,
			Executed:     true,
			Verified:     true,
			Output:       "deployment.apps/grafana restarted",
			VerifyOutput: "successfully rolled out",
		}
	}

	var prTargets []string
	dailyPRRemediator = func(ctx context.Context, issueNumber int, alertName, ns, target, workloadBase, diagnosis, telemetry string) (string, int) {
		prTargets = append(prTargets, ns+"/"+target)
		return "ready_for_human_merge", 901
	}

	var memosNotes []string
	memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error {
		mu.Lock()
		memosNotes = append(memosNotes, content)
		mu.Unlock()
		return nil
	}

	executeDailyReport(context.Background())

	if strings.Contains(dailyIssueBody, "PROACTIVE_ACTION:") {
		t.Errorf("expected PROACTIVE_ACTION directives stripped from daily issue body:\n%s", dailyIssueBody)
	}
	if runtimeCalls.Load() != 1 {
		t.Errorf("expected 1 proactive runtime fix executed, got %d", runtimeCalls.Load())
	}
	if len(prTargets) != 1 || prTargets[0] != "observability/kube-state-metrics" {
		t.Errorf("expected only [observability/kube-state-metrics] PR fix within cap=2 after skipping duplicate, got %v", prTargets)
	}
	if !strings.Contains(dailySummaryComment, "Automated Remediation Results") ||
		!strings.Contains(dailySummaryComment, "runtime_verified") ||
		!strings.Contains(dailySummaryComment, "ready_for_human_merge") ||
		!strings.Contains(dailySummaryComment, "Issue #802") ||
		!strings.Contains(dailySummaryComment, "Issue #803, PR #901") {
		t.Errorf("expected consolidated proactive results comment on daily issue #801, got:\n%s", dailySummaryComment)
	}
	if len(createdTitles) > 0 && strings.Contains(strings.Join(createdTitles, ","), "vietcalendar") {
		t.Errorf("expected apps/vietcalendar skipped past DAILY_MAX_PROACTIVE_FIXES cap, got titles=%v", createdTitles)
	}

	mReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mRR := httptest.NewRecorder()
	metrics.HandleMetrics(mRR, mReq)
	mBody := mRR.Body.String()
	if !strings.Contains(mBody, `commander_proactive_fixes_total{mode="runtime",outcome="verified"} 1`) ||
		!strings.Contains(mBody, `commander_proactive_fixes_total{mode="pr",outcome="ready_for_human_merge"} 1`) {
		t.Errorf("expected proactive fix metrics exported, got:\n%s", mBody)
	}
}

func TestExecuteDailyReport_DisabledAndFailureIsolation(t *testing.T) {
	origClient := github.Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	origMemos := config.MemosDBDSN
	origEnable := config.EnableDailyProactiveFix
	origCap := config.DailyMaxProactiveFixes
	origSnap := dailySnapshotCollector
	origSum := dailyIncidentSummarizer
	origEval := dailyReportEvaluator
	origExec := runtimeActionExecutor
	origInsert := memosInsertFn
	defer func() {
		github.Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
		config.MemosDBDSN = origMemos
		config.EnableDailyProactiveFix = origEnable
		config.DailyMaxProactiveFixes = origCap
		dailySnapshotCollector = origSnap
		dailyIncidentSummarizer = origSum
		dailyReportEvaluator = origEval
		runtimeActionExecutor = origExec
		memosInsertFn = origInsert
		dailyReportRunning.Store(false)
	}()

	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"
	config.MemosDBDSN = ""

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues"):
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls"):
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues":
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 810})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		}
	}))
	defer srv.Close()

	github.Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	github.Client.BaseURL = baseURL

	dailySnapshotCollector = func(ctx context.Context, workspaceDir, vmURL string) string { return "snap" }
	dailyIncidentSummarizer = func(window time.Duration) string { return "sum" }
	dailyReportEvaluator = func(ctx context.Context, dateStr, snapshot, incSummary string) (string, bool, error) {
		return "PROACTIVE_ACTION: FIX_RUNTIME | ROLLOUT_RESTART observability/deployment/grafana | lock\nPROACTIVE_ACTION: FIX_RUNTIME | DELETE_FAILED_JOB system/job/daily-old | prune\nHEALTH_STATUS: ATTENTION_NEEDED", false, nil
	}
	memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error { return nil }

	var execTargets []string
	runtimeActionExecutor = func(ctx context.Context, act k8s.RuntimeAction) k8s.RuntimeActionResult {
		execTargets = append(execTargets, act.Name)
		if act.Name == "grafana" {
			return k8s.RuntimeActionResult{Action: act, RBACDenied: true, Err: errors.New("forbidden")}
		}
		return k8s.RuntimeActionResult{Action: act, Executed: true, Verified: true}
	}

	// Case 1: Disabled flag triggers 0 actions
	config.EnableDailyProactiveFix = false
	config.DailyMaxProactiveFixes = 3
	executeDailyReport(context.Background())
	if len(execTargets) != 0 {
		t.Fatalf("expected 0 runtime calls when ENABLE_DAILY_PROACTIVE_FIX=false, got %v", execTargets)
	}

	// Case 2: Candidate 1 RBAC failure must NOT prevent candidate 2 from executing
	config.EnableDailyProactiveFix = true
	executeDailyReport(context.Background())
	if len(execTargets) != 2 || execTargets[0] != "grafana" || execTargets[1] != "daily-old" {
		t.Fatalf("expected both candidates attempted despite first failure, got %v", execTargets)
	}
}
