package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/db"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/incident"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/minion"
)

var (
	dailyReportRunning atomic.Bool
	dailyReportRunner  = executeDailyReport
)

// HandleDailyReport triggers an asynchronous read-only agy fleet check and publishes
// the report to Memos and GitHub Issues.
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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

func executeDailyReport(ctx context.Context) {
	ict := time.FixedZone("ICT", 7*3600)
	dateStr := time.Now().In(ict).Format("2006-01-02")

	snapshot := k8s.CollectDailyFleetSnapshot(ctx, config.WorkspaceDir, config.VictoriaMetricsURL)
	incSummary := incident.SummarizeRecentIncidents(24 * time.Hour)

	report, healthy, err := minion.RunDailyReport(ctx, dateStr, snapshot, incSummary)
	if err != nil {
		log.Printf("🚨 Daily fleet report failed: %v", err)
		if github.Client != nil {
			title := fmt.Sprintf("[Daily Fleet Check] %s (ERROR)", dateStr)
			body := fmt.Sprintf("## Daily Fleet Check Error (%s)\n\nError running `agy` audit: `%v`\n\n### Collected Snapshot\n```text\n%s\n```", dateStr, err, snapshot)
			if issue, issueErr := github.CreateIssue(title, body, []string{"daily-report", "needs-human-review"}); issueErr == nil && issue != nil {
				_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner)
			}
		}
		return
	}

	statusLabel := "ATTENTION NEEDED"
	if healthy {
		statusLabel = "HEALTHY"
	}

	if config.MemosDBDSN != "" {
		note := db.FormatDailyMemosNote(dateStr, statusLabel, report)
		if err := db.InsertMemosReport(ctx, config.MemosDBDSN, note, false); err != nil {
			log.Printf("⚠️ Failed inserting daily report into Memos: %v", err)
		} else {
			log.Printf("📝 Daily fleet report (%s) delivered to Memos.", dateStr)
		}
	}

	if github.Client != nil {
		title := fmt.Sprintf("[Daily Fleet Check] %s (%s)", dateStr, statusLabel)
		labels := []string{"daily-report"}
		if !healthy {
			labels = append(labels, "needs-human-review")
		}
		issue, issueErr := github.CreateIssue(title, report, labels)
		if issueErr != nil {
			log.Printf("⚠️ Failed creating daily report GitHub issue: %v", issueErr)
			return
		}
		if issue != nil {
			if healthy {
				if closeErr := github.CloseIssue(issue.GetNumber(), "✅ Daily fleet audit reported HEALTHY. Auto-closing; full report archived in Memos."); closeErr != nil {
					log.Printf("⚠️ Failed auto-closing healthy daily report issue #%d: %v", issue.GetNumber(), closeErr)
				}
			} else {
				_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner)
			}
		}
	}
}
