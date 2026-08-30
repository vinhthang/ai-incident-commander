package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/minion"
	"vinhthang.dev/ai-incident-commander/internal/state"
	"vinhthang.dev/ai-incident-commander/internal/workspace"
)

type GrafanaAlertPayload struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"alerts"`
}

var tracer = otel.Tracer("ai-incident-commander/webhook")

func HandleWebhook(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "HandleWebhook")
	defer span.End()

	var payload GrafanaAlertPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		span.RecordError(err)
		span.SetStatus(1, err.Error()) // 1 = Error
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	span.SetAttributes(attribute.Int("alerts.count", len(payload.Alerts)))

	for _, alert := range payload.Alerts {
		alertName := alert.Labels["alertname"]
		if alertName == "" {
			alertName = "UnknownAlert"
		}

		go func(bgCtx context.Context, aStatus, aName string, aLabels, aAnnotations map[string]string) {
			if !state.AcquireIncidentLock(aName, aStatus) {
				return // Dropped or debounced
			}
			processAlert(bgCtx, aStatus, aName, aLabels, aAnnotations)
		}(context.WithoutCancel(ctx), alert.Status, alertName, alert.Labels, alert.Annotations)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Webhook received"))
}

func processAlert(ctx context.Context, status, alertName string, labels, annotations map[string]string) {
	ctx, span := tracer.Start(ctx, "processAlert")
	defer span.End()
	defer state.ReleaseIncidentLock(alertName, "COOLING_DOWN")

	span.SetAttributes(
		attribute.String("alert.name", alertName),
		attribute.String("alert.status", status),
	)

	if status == "resolved" {
		log.Printf("Alert %s resolved (closing tracking logic omitted for brevity in PR lifecycle mode)", alertName)
		span.AddEvent("Alert resolved. Tracking removed.")
		state.StateMu.Lock()
		delete(state.ActiveIncidents, alertName)
		state.StateMu.Unlock()
		return
	}

	span.AddEvent("Fetching Telemetry")
	telemetry := k8s.FetchTelemetry(labels)
	
	log.Printf("New alert %s. Running Triage Minion...", alertName)
	span.AddEvent("Running Triage Minion")
	diagnosis := minion.RunTriage(ctx, alertName, labels, annotations, telemetry)

	if diagnosis == "IGNORED" {
		log.Printf("Triage Minion ignored alert %s. Halting.", alertName)
		span.AddEvent("Alert IGNORED by Triage Minion. Halting.")
		return
	}

	span.AddEvent("Creating GitHub Issue")
	issueTitle := fmt.Sprintf("[Alert] %s", alertName)
	issueBody := fmt.Sprintf("## Grafana Alert: %s\n\n**Diagnosis from Triage Minion:**\n%s\n\n**Telemetry:**\n```text\n%s\n```", alertName, diagnosis, telemetry)
	
	issue, err := github.CreateIssue(issueTitle, issueBody, []string{"ai-incident"})
	if err != nil {
		log.Printf("Error creating issue: %v", err)
		span.RecordError(err)
		return
	}
	span.SetAttributes(attribute.Int("github.issue", issue.GetNumber()))

	log.Printf("Created issue #%d. Creating branch...", issue.GetNumber())
	span.AddEvent("Resetting Workspace")
	workspace.ResetToMain()
	
	branchName := fmt.Sprintf("fix/alert-%d-%d", issue.GetNumber(), time.Now().Unix())
	span.SetAttributes(attribute.String("git.branch", branchName))
	
	if err := workspace.CreateAndCheckoutBranch(branchName); err != nil {
		log.Printf("Failed to create branch: %v", err)
		span.RecordError(err)
		return
	}

	log.Printf("Triggering Fixer Minion on branch %s...", branchName)
	span.AddEvent("Running Fixer Minion")
	fixerLogs := minion.RunFixer(ctx, issue.GetNumber(), branchName, alertName, diagnosis, telemetry)
	_ = github.AddIssueComment(issue.GetNumber(), fixerLogs)

	log.Printf("Creating Pull Request...")
	span.AddEvent("Creating Pull Request")
	prTitle := fmt.Sprintf("Fix for %s (Issue #%d)", alertName, issue.GetNumber())
	prBody := fmt.Sprintf("Automated PR by AI Fixer Minion.\nCloses #%d\n\n### Fixer Logs\n```text\n%s\n```", issue.GetNumber(), fixerLogs)
	
	pr, err := github.CreatePullRequest(branchName, prTitle, prBody)
	if err != nil {
		log.Printf("Error creating PR: %v", err)
		span.RecordError(err)
		return
	}
	span.SetAttributes(attribute.Int("github.pr", pr.GetNumber()))

	log.Printf("Fetching PR Diff for #%d...", pr.GetNumber())
	span.AddEvent("Fetching PR Diff")
	diff, _ := github.GetPullRequestDiff(pr.GetNumber())

	log.Printf("Triggering Reviewer Minion for PR #%d...", pr.GetNumber())
	span.AddEvent("Running Reviewer Minion")
	approved, reviewComments := minion.RunReviewer(ctx, pr.GetNumber(), branchName, diff, diagnosis)

	if approved {
		log.Printf("PR #%d Approved! Merging...", pr.GetNumber())
		span.AddEvent("PR Approved. Merging")
		_ = github.MergePullRequest(pr.GetNumber())
		_ = github.AddIssueComment(pr.GetNumber(), "✅ **Reviewer Minion Approved**: The diff looks good and addresses the triage diagnosis. Merging automatically.")
	} else {
		log.Printf("PR #%d Rejected. Halting and assigning human...", pr.GetNumber())
		span.AddEvent("PR Rejected. Assigning human.")
		rejectionMsg := fmt.Sprintf("❌ **Reviewer Minion Rejected**: The fix is inadequate, unsafe, or introduces regressions.\n\n### Feedback\n```text\n%s\n```\n\nHalting AI automation and assigning a human for manual review.", reviewComments)
		_ = github.AddIssueComment(pr.GetNumber(), rejectionMsg)
		_ = github.AssignIssueOrPR(pr.GetNumber(), config.GithubOwner)
		_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner) // Assign original issue too
	}
}
