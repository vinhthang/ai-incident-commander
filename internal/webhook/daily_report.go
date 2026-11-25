package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/trace"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/db"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/incident"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/metrics"
	"vinhthang.dev/ai-incident-commander/internal/minion"
)

var (
	dailyReportRunning      atomic.Bool
	dailyReportRunner       = executeDailyReport
	dailyReportTimeout      = 15 * time.Minute
	dailySnapshotCollector  = k8s.CollectDailyFleetSnapshot
	dailyIncidentSummarizer = incident.SummarizeRecentIncidents
	dailyReportEvaluator    = minion.RunDailyReport
	dailyDiagnosticBundleFn = func(labels map[string]string) string {
		return k8s.FetchDiagnosticBundle(labels, config.WorkspaceDir)
	}
	dailyPRRemediator = func(ctx context.Context, issueNumber int, alertName, ns, target, workloadBase, diagnosis, telemetry string) (string, int) {
		return remediateClusterIssue(ctx, trace.SpanFromContext(ctx), issueNumber, "daily-report", alertName, ns, target, workloadBase, diagnosis, telemetry)
	}
)

// HandleDailyReport triggers an asynchronous read-only agy fleet check, publishes
// the report to Memos (#DailyFleetCheck), and dispatches bounded proactive remediations if enabled.
func HandleDailyReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if config.WebhookAuthToken != "" && !isAuthorizedBearerToken(r.Header.Get("Authorization"), config.WebhookAuthToken) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !dailyReportRunning.CompareAndSwap(false, true) {
		http.Error(w, "Daily report already in progress", http.StatusConflict)
		return
	}

	go func() {
		defer dailyReportRunning.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), dailyReportTimeout)
		defer cancel()
		dailyReportRunner(ctx)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "accepted",
		"message": "Daily fleet report started",
	})
}

func deliverDailyMemosNote(ctx context.Context, dateStr, statusLabel, body string) {
	if config.MemosDBDSN == "" {
		return
	}
	note := db.FormatDailyMemosNote(dateStr, statusLabel, body)
	if err := memosInsertFn(ctx, config.MemosDBDSN, note, false); err != nil {
		log.Printf("⚠️ Failed inserting daily report (%s) into Memos: %v", statusLabel, err)
		return
	}
	log.Printf("📝 Daily fleet report (%s, %s) delivered to Memos.", dateStr, statusLabel)
}

func executeDailyReport(ctx context.Context) {
	ict := time.FixedZone("ICT", 7*3600)
	dateStr := time.Now().In(ict).Format("2006-01-02")

	snapshot := dailySnapshotCollector(ctx, config.WorkspaceDir, config.VictoriaMetricsURL)
	incSummary := dailyIncidentSummarizer(24 * time.Hour)

	report, healthy, err := dailyReportEvaluator(ctx, dateStr, snapshot, incSummary)
	if err != nil {
		metrics.IncDailyReport("error")
		log.Printf("🚨 Daily fleet report failed: %v", err)
		errBody := fmt.Sprintf("## Daily Fleet Check Error (%s)\n\nError running `agy` audit: `%v`\n\n### Collected Snapshot\n```text\n%s\n```", dateStr, err, snapshot)
		deliverDailyMemosNote(ctx, dateStr, "ERROR", errBody)
		return
	}

	cleanReport, proactiveActions := minion.ParseDailyProactiveActions(report)

	statusLabel := "ATTENTION NEEDED"
	if healthy {
		statusLabel = "HEALTHY"
		metrics.IncDailyReport("healthy")
	} else {
		metrics.IncDailyReport("attention_needed")
	}

	finalReport := cleanReport
	if config.EnableDailyProactiveFix && config.DailyMaxProactiveFixes > 0 && len(proactiveActions) > 0 {
		if summary := dispatchDailyProactiveFixes(ctx, dateStr, proactiveActions); summary != "" {
			finalReport = strings.TrimSpace(cleanReport) + "\n\n" + summary
		}
	}

	deliverDailyMemosNote(ctx, dateStr, statusLabel, finalReport)
}

func dispatchDailyProactiveFixes(ctx context.Context, dateStr string, actions []minion.DailyProactiveAction) string {
	var lines []string
	dispatched := 0

	for _, act := range actions {
		if dispatched >= config.DailyMaxProactiveFixes {
			lines = append(lines, fmt.Sprintf("- Skipped **%s** `%s/%s`: reached `DAILY_MAX_PROACTIVE_FIXES=%d` cap.",
				act.Mode, act.Namespace, act.Target, config.DailyMaxProactiveFixes))
			continue
		}

		workloadBase := incident.NormalizeWorkloadBase(act.Target)
		fp := incident.ComputeFingerprint("ProactiveDaily", act.Namespace, act.Target)
		if github.Client != nil && checkExistingOpenGitHubWork(ctx, fp, "ProactiveDaily", act.Namespace, act.Target, workloadBase) {
			lines = append(lines, fmt.Sprintf("- Skipped **%s** `%s/%s`: existing open GitHub Issue or PR already active.",
				act.Mode, act.Namespace, act.Target))
			continue
		}

		dispatched++
		line := dispatchSingleProactiveFix(ctx, dateStr, workloadBase, act)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		return ""
	}
	return "### Automated Remediation Results\n\n" + strings.Join(lines, "\n")
}

func dispatchSingleProactiveFix(ctx context.Context, dateStr, workloadBase string, act minion.DailyProactiveAction) string {
	if act.Mode == minion.ProactiveFixRuntime && act.RuntimeAction != nil {
		target := act.RuntimeAction.TargetString()
		issueNum := 0
		if github.Client != nil {
			title := fmt.Sprintf("[Proactive Runtime Fix] %s (%s)", target, dateStr)
			body := fmt.Sprintf("## Proactive Daily Audit Runtime Fix\n\n- **Target**: `%s`\n- **Action**: `%s`\n- **Root Cause**: %s",
				target, act.RuntimeAction.Type, act.Diagnosis)
			issue, err := github.CreateIssue(ctx, title, body, []string{"ai-incident", "proactive-fix"})
			if err != nil {
				log.Printf("⚠️ Failed creating proactive runtime issue for %s: %v", target, err)
			} else if issue != nil {
				issueNum = issue.GetNumber()
			}
		}

		res := executeAndReportRuntimeFix(ctx, "daily-report", "", issueNum, *act.RuntimeAction, act.Diagnosis)
		metricOutcome := "failed"
		if res.Verified {
			metricOutcome = "verified"
		} else if res.RBACDenied {
			metricOutcome = "rbac_denied"
		}
		metrics.IncProactiveFix("runtime", metricOutcome)

		statusToken := "runtime_" + metricOutcome
		if issueNum > 0 {
			return fmt.Sprintf("- **FIX_RUNTIME** `%s`: `%s` (Issue #%d)", target, statusToken, issueNum)
		}
		return fmt.Sprintf("- **FIX_RUNTIME** `%s`: `%s`", target, statusToken)
	}

	// ProactiveFixViaPR
	targetLabel := fmt.Sprintf("%s/%s", act.Namespace, act.Target)
	labels := map[string]string{
		"namespace":  act.Namespace,
		"app":        workloadBase,
		"deployment": workloadBase,
	}
	telemetry := dailyDiagnosticBundleFn(labels)
	if strings.TrimSpace(telemetry) == "" {
		telemetry = act.Diagnosis
	}

	issueNum := 0
	if github.Client != nil {
		title := fmt.Sprintf("[Proactive Fix] %s (%s)", targetLabel, dateStr)
		body := fmt.Sprintf("## Proactive Daily Audit GitOps Fix\n\n- **Target**: `%s`\n- **Root Cause**: %s\n\n<details>\n<summary>Collected Diagnostic Telemetry</summary>\n\n```text\n%s\n```\n</details>",
			targetLabel, act.Diagnosis, telemetry)
		issue, err := github.CreateIssue(ctx, title, body, []string{"ai-incident", "proactive-fix"})
		if err != nil {
			log.Printf("⚠️ Failed creating proactive PR tracking issue for %s: %v", targetLabel, err)
			metrics.IncProactiveFix("pr", "issue_error")
			return fmt.Sprintf("- **FIX_VIA_PR** `%s`: `issue_error` (%v)", targetLabel, err)
		}
		if issue != nil {
			issueNum = issue.GetNumber()
		}
	}

	outcome, prNum := dailyPRRemediator(ctx, issueNum, "ProactiveFix", act.Namespace, act.Target, workloadBase, act.Diagnosis, telemetry)
	metrics.IncProactiveFix("pr", outcome)

	if prNum > 0 {
		return fmt.Sprintf("- **FIX_VIA_PR** `%s`: `%s` (Issue #%d, PR #%d)", targetLabel, outcome, issueNum, prNum)
	}
	if issueNum > 0 {
		return fmt.Sprintf("- **FIX_VIA_PR** `%s`: `%s` (Issue #%d)", targetLabel, outcome, issueNum)
	}
	return fmt.Sprintf("- **FIX_VIA_PR** `%s`: `%s`", targetLabel, outcome)
}
