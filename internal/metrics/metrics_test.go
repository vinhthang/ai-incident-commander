package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleMetrics_PrometheusExpositionAndMethodCheck(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	IncAlertsReceived("firing")
	IncAlertsReceived("firing")
	IncAlertsReceived("resolved")
	IncTriageVerdict("FIX_VIA_PR")
	IncRemediationOutcome("ready_for_human_merge")
	IncDailyReport("healthy")
	IncAgyJob("success")
	IncAgyJob("skipped_expired")
	IncAgyJob(`quoted"\nlbl`)
	ObserveAgyDuration(4.2)
	ObserveAgyDuration(22.5)
	SetQueueDepthFunc(func() int { return 3 })

	// Method check: POST /metrics -> 405
	postReq := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	postRR := httptest.NewRecorder()
	HandleMetrics(postRR, postReq)
	if postRR.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed on POST /metrics, got %d", postRR.Code)
	}

	// GET /metrics -> 200 with Prometheus text format
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	HandleMetrics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /metrics, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain Content-Type, got %q", ct)
	}

	body := rr.Body.String()
	wants := []string{
		"# TYPE commander_alerts_total counter",
		`commander_alerts_total{status="firing"} 2`,
		`commander_alerts_total{status="resolved"} 1`,
		"# TYPE commander_triage_verdicts_total counter",
		`commander_triage_verdicts_total{verdict="FIX_VIA_PR"} 1`,
		"# TYPE commander_remediation_outcomes_total counter",
		`commander_remediation_outcomes_total{outcome="ready_for_human_merge"} 1`,
		"# TYPE commander_daily_reports_total counter",
		`commander_daily_reports_total{status="healthy"} 1`,
		"# TYPE commander_agy_jobs_total counter",
		`commander_agy_jobs_total{outcome="success"} 1`,
		`commander_agy_jobs_total{outcome="skipped_expired"} 1`,
		`commander_agy_jobs_total{outcome="quoted\"\\nlbl"} 1`,
		"# TYPE commander_agy_duration_seconds histogram",
		`commander_agy_duration_seconds_bucket{le="5"} 1`,
		`commander_agy_duration_seconds_bucket{le="15"} 1`,
		`commander_agy_duration_seconds_bucket{le="30"} 2`,
		`commander_agy_duration_seconds_bucket{le="+Inf"} 2`,
		"commander_agy_duration_seconds_sum 26.7",
		"commander_agy_duration_seconds_count 2",
		"# TYPE commander_queue_depth gauge",
		"commander_queue_depth 3",
	}
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}
}

func TestHandleMetrics_ProactiveAndRuntimeOutcomes(t *testing.T) {
	ResetForTest()
	defer ResetForTest()

	IncRemediationOutcome("runtime_verified")
	IncProactiveFix("runtime", "verified")
	IncProactiveFix("runtime", "failed")
	IncProactiveFix("pr", "ready_for_human_merge")
	IncProactiveFix(`m"1`, `ok\n2`)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	HandleMetrics(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET /metrics, got %d", rr.Code)
	}

	body := rr.Body.String()
	for _, want := range []string{
		`commander_remediation_outcomes_total{outcome="runtime_verified"} 1`,
		"# TYPE commander_proactive_fixes_total counter",
		`commander_proactive_fixes_total{mode="runtime",outcome="verified"} 1`,
		`commander_proactive_fixes_total{mode="runtime",outcome="failed"} 1`,
		`commander_proactive_fixes_total{mode="pr",outcome="ready_for_human_merge"} 1`,
		`commander_proactive_fixes_total{mode="m\"1",outcome="ok\\n2"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q:\n%s", want, body)
		}
	}

	idxPR := strings.Index(body, `commander_proactive_fixes_total{mode="pr",outcome="ready_for_human_merge"}`)
	idxRTFailed := strings.Index(body, `commander_proactive_fixes_total{mode="runtime",outcome="failed"}`)
	idxRTVerified := strings.Index(body, `commander_proactive_fixes_total{mode="runtime",outcome="verified"}`)
	if idxPR >= idxRTFailed || idxRTFailed >= idxRTVerified {
		t.Errorf("expected deterministic lexicographic order pr < runtime/failed < runtime/verified, got indices pr=%d rtFailed=%d rtVerified=%d\n%s",
			idxPR, idxRTFailed, idxRTVerified, body)
	}
}
