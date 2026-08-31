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

		go func(detachedCtx context.Context, aStatus, aName string, aLabels, aAnnotations map[string]string) {
			if !state.AcquireIncidentLock(aName, aStatus) {
				return // Dropped or debounced
			}
			// Enforce 10-minute overall lifecycle timeout to prevent leaked goroutines
			bgCtx, cancel := context.WithTimeout(detachedCtx, 10*time.Minute)
			defer cancel()

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
		log.Printf("Alert %s resolved. Cleaning active incident state.", alertName)
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

	if labels["namespace"] == "apps" {
		serviceName := labels["service"]
		if serviceName == "" {
			serviceName = labels["app"]
		}
		endpointOrTopic := labels["endpoint"]
		if endpointOrTopic == "" {
			endpointOrTopic = labels["topic"]
		}
		statusCode := labels["status_code"]
		if statusCode == "" {
			statusCode = "UNKNOWN"
		}
		traceID := labels["trace_id"]
		if traceID == "" {
			traceID = annotations["trace_id"]
		}

		log.Printf("Routing incident to AppFixer (Service: %s)", serviceName)
		span.AddEvent("Routing to AppFixer")
		err := minion.AppFixer(serviceName, endpointOrTopic, statusCode, traceID, diagnosis)
		if err != nil {
			log.Printf("AppFixer encountered an error: %v", err)
			span.RecordError(err)
		}
		return // Do not create a GitHub issue or PR for App errors right now
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
	diff, err := github.GetPullRequestDiff(pr.GetNumber())
	if err != nil {
		log.Printf("Error fetching PR diff for #%d: %v", pr.GetNumber(), err)
		span.RecordError(err)
		_ = github.AddIssueComment(pr.GetNumber(), fmt.Sprintf("⚠️ Unable to fetch PR diff: %v. Requesting human review.", err))
		_ = github.AssignIssueOrPR(pr.GetNumber(), config.GithubOwner)
		return
	}

	log.Printf("Triggering Reviewer Minion for PR #%d...", pr.GetNumber())
	span.AddEvent("Running Reviewer Minion")
	approved, reviewComments := minion.RunReviewer(ctx, pr.GetNumber(), branchName, diff, diagnosis)

	if approved {
		log.Printf("PR #%d Approved! Merging...", pr.GetNumber())
		span.AddEvent("PR Approved. Merging")
		if mergeErr := github.MergePullRequest(pr.GetNumber()); mergeErr != nil {
			log.Printf("Failed to auto-merge PR #%d: %v", pr.GetNumber(), mergeErr)
			span.RecordError(mergeErr)
			_ = github.AddIssueComment(pr.GetNumber(), fmt.Sprintf("⚠️ **Reviewer Approved**, but auto-merge failed: %v. Please merge manually.", mergeErr))
			_ = github.AssignIssueOrPR(pr.GetNumber(), config.GithubOwner)
		} else {
			_ = github.AddIssueComment(pr.GetNumber(), "✅ **Reviewer Minion Approved**: The diff looks good and addresses the triage diagnosis. Merged automatically.")
		}
	} else {
		log.Printf("PR #%d Rejected or unverified. Halting and assigning human...", pr.GetNumber())
		span.AddEvent("PR Rejected. Assigning human.")
		rejectionMsg := fmt.Sprintf("❌ **Reviewer Minion Rejected**: The fix is unverified, inadequate, or requires human oversight.\n\n### Feedback\n```text\n%s\n```\n\nHalting AI automation and assigning a human for manual review.", reviewComments)
		_ = github.AddIssueComment(pr.GetNumber(), rejectionMsg)
		_ = github.AssignIssueOrPR(pr.GetNumber(), config.GithubOwner)
		_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner)
	}
}
