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

func TestHandleDailyReport_AuthAndMethod(t *testing.T) {
	origToken := config.WebhookAuthToken
	origRunner := dailyReportRunner
	defer func() {
		config.WebhookAuthToken = origToken
		dailyReportRunner = origRunner
		dailyReportRunning.Store(false)
	}()

	// Method check: GET rejected with 405
	reqGet := httptest.NewRequest(http.MethodGet, "/daily-report", nil)
	rrGet := httptest.NewRecorder()
	HandleDailyReport(rrGet, reqGet)
	if rrGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on GET /daily-report, got %d", rrGet.Code)
	}

	// Auth check: unauthenticated POST rejected with 401 when token set
	config.WebhookAuthToken = "daily-secret"
	reqUnauth := httptest.NewRequest(http.MethodPost, "/daily-report", nil)
	rrUnauth := httptest.NewRecorder()
	HandleDailyReport(rrUnauth, reqUnauth)
	if rrUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauth POST /daily-report, got %d", rrUnauth.Code)
	}

	// Valid POST returns 202 Accepted and guards against concurrent duplicate
	started := make(chan struct{})
	release := make(chan struct{})
	dailyReportRunner = func(ctx context.Context) {
		close(started)
		<-release
	}

	reqOK := httptest.NewRequest(http.MethodPost, "/daily-report", nil)
	reqOK.Header.Set("Authorization", "Bearer daily-secret")
	rrOK := httptest.NewRecorder()
	HandleDailyReport(rrOK, reqOK)
	if rrOK.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on POST /daily-report, got %d: %s", rrOK.Code, rrOK.Body.String())
	}

	<-started

	reqDup := httptest.NewRequest(http.MethodPost, "/daily-report", nil)
	reqDup.Header.Set("Authorization", "Bearer daily-secret")
	rrDup := httptest.NewRecorder()
	HandleDailyReport(rrDup, reqDup)
	if rrDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict while daily report is in progress, got %d", rrDup.Code)
	}

	close(release)
}

func TestHandleWebhook_MethodAndMaxBodySize(t *testing.T) {
	origToken := config.WebhookAuthToken
	defer func() { config.WebhookAuthToken = origToken }()
	config.WebhookAuthToken = ""

	reqGet := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rrGet := httptest.NewRecorder()
	HandleWebhook(rrGet, reqGet)
	if rrGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed on GET /webhook, got %d", rrGet.Code)
	}

	oversized := `{"status":"firing","alerts":[{"status":"firing","labels":{"padding":"` + strings.Repeat("a", (2<<20)+64) + `"}}]}`
	reqBig := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(oversized))
	rrBig := httptest.NewRecorder()
	HandleWebhook(rrBig, reqBig)
	if rrBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Request Entity Too Large for >2MiB body, got %d: %s", rrBig.Code, rrBig.Body.String())
	}
}

func TestPublishOpenAppIncident(t *testing.T) {
	origClient := github.Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	origMemos := config.MemosDBDSN
	defer func() {
		github.Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
		config.MemosDBDSN = origMemos
	}()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"
	config.MemosDBDSN = ""

	var createdIssues atomic.Int32
	var addedComments atomic.Int32
	var assignedIssue atomic.Int32
	var hasExistingIssue atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/vinhthang/oci/issues":
			if hasExistingIssue.Load() {
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"number": 701, "title": "[App Alert] apps/orders: /api/pay (500)"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues":
			createdIssues.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 701})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues/701/assignees":
			assignedIssue.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 701})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues/701/comments":
			addedComments.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	github.Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	github.Client.BaseURL = baseURL

	// Ignored app incident must not open issues or comment
	publishOpenAppIncident(context.Background(), "HTTP500", "orders", "/api/pay", "500", "tr-1", "ignored", "noise")
	if createdIssues.Load() != 0 || addedComments.Load() != 0 {
		t.Fatalf("expected ignored app incident to publish nothing, created=%d comments=%d", createdIssues.Load(), addedComments.Load())
	}

	// Open app incident with no existing issue creates issue and assigns owner
	publishOpenAppIncident(context.Background(), "HTTP500", "orders", "/api/pay", "500", "tr-1", "open", "Null pointer")
	if createdIssues.Load() != 1 || assignedIssue.Load() != 1 {
		t.Fatalf("expected 1 created issue and 1 assignee, got created=%d assigned=%d", createdIssues.Load(), assignedIssue.Load())
	}

	// Open app incident when existing issue is already open comments instead of duplicating
	hasExistingIssue.Store(true)
	publishOpenAppIncident(context.Background(), "HTTP500", "orders", "/api/pay", "500", "tr-2", "open", "Null pointer again")
	if createdIssues.Load() != 1 || addedComments.Load() != 1 {
		t.Fatalf("expected comment on existing open app issue without duplicate creation, created=%d comments=%d", createdIssues.Load(), addedComments.Load())
	}
}

func TestExecuteAndReportRuntimeFix_SuccessAutoClosesAndLogsMemos(t *testing.T) {
	origClient := github.Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	origMemos := config.MemosDBDSN
	origInsert := memosInsertFn
	origExec := runtimeActionExecutor
	origResolve := clusterResolveFn
	defer func() {
		github.Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
		config.MemosDBDSN = origMemos
		memosInsertFn = origInsert
		runtimeActionExecutor = origExec
		clusterResolveFn = origResolve
		metrics.ResetForTest()
	}()

	metrics.ResetForTest()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"
	config.MemosDBDSN = "postgres://memos-test/db"

	var closedIssue atomic.Bool
	var commentAdded atomic.Bool
	var closedBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/vinhthang/oci/issues/555/comments":
			commentAdded.Store(true)
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			closedBody = payload["body"]
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/vinhthang/oci/issues/555":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["state"] == "closed" {
				closedIssue.Store(true)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 555, "state": "closed"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	github.Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	github.Client.BaseURL = baseURL

	var resolvedFP string
	clusterResolveFn = func(fp string) (int, error) {
		resolvedFP = fp
		return 555, nil
	}

	var mu sync.Mutex
	var memosNotes []string
	memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error {
		mu.Lock()
		memosNotes = append(memosNotes, content)
		mu.Unlock()
		return nil
	}

	runtimeActionExecutor = func(ctx context.Context, act k8s.RuntimeAction) k8s.RuntimeActionResult {
		return k8s.RuntimeActionResult{
			Action:       act,
			Executed:     true,
			Verified:     true,
			Output:       "deployment.apps/grafana restarted",
			VerifyOutput: `deployment "grafana" successfully rolled out`,
		}
	}

	act := k8s.RuntimeAction{
		Type:      k8s.RuntimeRolloutRestart,
		Namespace: "observability",
		Kind:      "deployment",
		Name:      "grafana",
	}
	res := executeAndReportRuntimeFix(context.Background(), "reactive", "fp-555", 555, act, "Grafana SQLite lock detected.")
	if !res.Verified {
		t.Fatalf("expected verified runtime fix, got %+v", res)
	}
	if !closedIssue.Load() || !commentAdded.Load() {
		t.Fatalf("expected issue #555 closed with comment, closed=%v comment=%v", closedIssue.Load(), commentAdded.Load())
	}
	if !strings.Contains(closedBody, "Auto-Remediation Verified") {
		t.Errorf("expected verification comment on closed issue, got %q", closedBody)
	}
	if resolvedFP != "fp-555" {
		t.Errorf("expected cluster_incidents ResolveClusterIncident called for fp-555, got %q", resolvedFP)
	}
	if len(memosNotes) != 1 || !strings.Contains(memosNotes[0], "#AutoRemediation") || !strings.Contains(memosNotes[0], "runtime_verified") {
		t.Errorf("expected #AutoRemediation runtime_verified note in Memos, got %v", memosNotes)
	}

	mReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mRR := httptest.NewRecorder()
	metrics.HandleMetrics(mRR, mReq)
	if !strings.Contains(mRR.Body.String(), `commander_remediation_outcomes_total{outcome="runtime_verified"} 1`) {
		t.Errorf("expected runtime_verified metric increment, got:\n%s", mRR.Body.String())
	}
}

func TestExecuteAndReportRuntimeFix_FailureAndRBACDeniedKeepIssueOpen(t *testing.T) {
	origClient := github.Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	origMemos := config.MemosDBDSN
	origInsert := memosInsertFn
	origExec := runtimeActionExecutor
	origResolve := clusterResolveFn
	defer func() {
		github.Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
		config.MemosDBDSN = origMemos
		memosInsertFn = origInsert
		runtimeActionExecutor = origExec
		clusterResolveFn = origResolve
		metrics.ResetForTest()
	}()

	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"
	config.MemosDBDSN = "postgres://memos-test/db"

	for _, tc := range []struct {
		name        string
		result      k8s.RuntimeActionResult
		wantOutcome string
	}{
		{
			name: "rbac denied keeps issue open and assigns owner",
			result: k8s.RuntimeActionResult{
				Action:     k8s.RuntimeAction{Type: k8s.RuntimeRolloutRestart, Namespace: "observability", Kind: "deployment", Name: "grafana"},
				RBACDenied: true,
				Output:     "Error from server (Forbidden): cannot patch deployments",
				Err:        errors.New("forbidden"),
			},
			wantOutcome: "runtime_rbac_denied",
		},
		{
			name: "verification timeout keeps issue open and assigns owner",
			result: k8s.RuntimeActionResult{
				Action:       k8s.RuntimeAction{Type: k8s.RuntimeRolloutRestart, Namespace: "observability", Kind: "deployment", Name: "grafana"},
				Executed:     true,
				Verified:     false,
				VerifyOutput: "timed out waiting for rollout status",
				Err:          errors.New("timeout"),
			},
			wantOutcome: "runtime_failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metrics.ResetForTest()
			var closed atomic.Bool
			var assigned atomic.Bool
			var commentBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/issues/556"):
					closed.Store(true)
					_ = json.NewEncoder(w).Encode(map[string]any{"number": 556})
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/556/comments"):
					var p map[string]string
					_ = json.NewDecoder(r.Body).Decode(&p)
					commentBody = p["body"]
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/556/assignees"):
					assigned.Store(true)
					_ = json.NewEncoder(w).Encode(map[string]any{"number": 556})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			github.Client = gh.NewClient(srv.Client())
			baseURL, _ := url.Parse(srv.URL + "/")
			github.Client.BaseURL = baseURL

			resolveCalled := false
			clusterResolveFn = func(fp string) (int, error) {
				resolveCalled = true
				return 0, nil
			}
			// Simulate Memos DB error to confirm GitHub assignment is not blocked
			memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error {
				return errors.New("memos unavailable")
			}
			runtimeActionExecutor = func(ctx context.Context, act k8s.RuntimeAction) k8s.RuntimeActionResult {
				return tc.result
			}

			res := executeAndReportRuntimeFix(context.Background(), "reactive", "fp-556", 556, tc.result.Action, "SQLite lock")
			if res.Verified {
				t.Fatalf("expected unverified result")
			}
			if closed.Load() || resolveCalled {
				t.Fatalf("failed/denied runtime fix must NOT close issue or resolve DB incident (closed=%v resolved=%v)", closed.Load(), resolveCalled)
			}
			if !assigned.Load() || commentBody == "" {
				t.Fatalf("expected comment and owner assignment on failed runtime fix (assigned=%v body=%q)", assigned.Load(), commentBody)
			}
			for _, substr := range []string{"Error:", tc.wantOutcome} {
				if !strings.Contains(commentBody, substr) {
					t.Errorf("expected commentBody to contain %q, got:\n%s", substr, commentBody)
				}
			}
			if tc.result.RBACDenied && (!strings.Contains(commentBody, "Forbidden") || !strings.Contains(commentBody, "Command output:")) {
				t.Errorf("expected RBAC Forbidden and Command output details in comment, got:\n%s", commentBody)
			}
			if !tc.result.RBACDenied && !strings.Contains(commentBody, "Verification output:") {
				t.Errorf("expected Verification output in failure comment, got:\n%s", commentBody)
			}

			mReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			mRR := httptest.NewRecorder()
			metrics.HandleMetrics(mRR, mReq)
			if !strings.Contains(mRR.Body.String(), `commander_remediation_outcomes_total{outcome="`+tc.wantOutcome+`"} 1`) {
				t.Errorf("missing metric outcome %q in:\n%s", tc.wantOutcome, mRR.Body.String())
			}
		})
	}
}

func TestReportRemediationOutcome_AutoMergeFalseAndMemosGracefulError(t *testing.T) {
	origMemos := config.MemosDBDSN
	origInsert := memosInsertFn
	defer func() {
		config.MemosDBDSN = origMemos
		memosInsertFn = origInsert
	}()

	// Case 1: Empty MEMOS_DB_DSN must no-op without calling memosInsertFn
	config.MemosDBDSN = ""
	called := false
	memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error {
		called = true
		return nil
	}
	reportRemediationOutcome(context.Background(), RemediationReport{
		Source:  "reactive",
		Mode:    "pr",
		Target:  "observability/grafana",
		Outcome: "ready_for_human_merge",
	})
	if called {
		t.Fatalf("expected memosInsertFn not called when MEMOS_DB_DSN is empty")
	}

	// Case 2: Configured DSN posts PR + Issue refs and logs cleanly even if Memos errors
	config.MemosDBDSN = "postgres://memos-test/db"
	var captured string
	memosInsertFn = func(ctx context.Context, dsn, content string, pinned bool) error {
		captured = content
		return errors.New("db write error")
	}
	reportRemediationOutcome(context.Background(), RemediationReport{
		Source:      "reactive",
		Mode:        "pr",
		Target:      "observability/grafana",
		Outcome:     "ready_for_human_merge",
		RootCause:   "Memory limit bump",
		Details:     "PR #702 awaiting human merge (AUTO_MERGE=false).",
		IssueNumber: 700,
		PRNumber:    702,
	})
	for _, want := range []string{"#AutoRemediation", "ready_for_human_merge", "#700", "#702", "AUTO_MERGE=false"} {
		if !strings.Contains(captured, want) {
			t.Errorf("expected Memos report to contain %q, got:\n%s", want, captured)
		}
	}

	// Case 3: IssueNumber == 0 and PRNumber == 0 render N/A instead of #0
	reportRemediationOutcome(context.Background(), RemediationReport{
		Source:    "daily-report",
		Mode:      "runtime",
		Target:    "system/job/daily-old",
		Outcome:   "runtime_verified",
		RootCause: "Completed cleanup",
		Details:   "Deleted failed job",
	})
	if strings.Contains(captured, "#0") || !strings.Contains(captured, "**GitHub Issue**: N/A") || !strings.Contains(captured, "**GitHub PR**: N/A") {
		t.Errorf("expected N/A for zero IssueNumber/PRNumber, got:\n%s", captured)
	}
}
