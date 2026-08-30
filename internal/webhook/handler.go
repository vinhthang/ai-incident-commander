package webhook

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

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

func HandleWebhook(w http.ResponseWriter, r *http.Request) {
	var payload GrafanaAlertPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, alert := range payload.Alerts {
		alertName := alert.Labels["alertname"]
		if alertName == "" {
			alertName = "UnknownAlert"
		}

		go func(aStatus, aName string, aLabels, aAnnotations map[string]string) {
			if !state.AcquireIncidentLock(aName, aStatus) {
				return // Dropped or debounced
			}
			processAlert(aStatus, aName, aLabels, aAnnotations)
		}(alert.Status, alertName, alert.Labels, alert.Annotations)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook received"))
}

func processAlert(status, alertName string, labels, annotations map[string]string) {
	defer state.ReleaseIncidentLock(alertName, "COOLING_DOWN")

	if status == "resolved" {
		log.Printf("Alert %s resolved (closing tracking logic omitted for brevity in PR lifecycle mode)", alertName)
		state.StateMu.Lock()
		delete(state.ActiveIncidents, alertName)
		state.StateMu.Unlock()
		return
	}

	telemetry := k8s.FetchTelemetry(labels)
	log.Printf("New alert %s. Running Triage Minion...", alertName)
	diagnosis := minion.RunTriage(alertName, labels, annotations, telemetry)

	if diagnosis == "IGNORED" {
		log.Printf("Triage Minion ignored alert %s. Halting.", alertName)
		return
	}

	issueTitle := fmt.Sprintf("[Alert] %s", alertName)
	issueBody := fmt.Sprintf("## Grafana Alert: %s\n\n**Diagnosis from Triage Minion:**\n%s\n\n**Telemetry:**\n```text\n%s\n```", alertName, diagnosis, telemetry)
	
	issue, err := github.CreateIssue(issueTitle, issueBody, []string{"ai-incident"})
	if err != nil {
		log.Printf("Error creating issue: %v", err)
		return
	}

	log.Printf("Created issue #%d. Creating branch...", issue.GetNumber())
	workspace.ResetToMain()
	
	branchName := fmt.Sprintf("fix/alert-%d-%d", issue.GetNumber(), time.Now().Unix())
	if err := workspace.CreateAndCheckoutBranch(branchName); err != nil {
		log.Printf("Failed to create branch: %v", err)
		return
	}

	log.Printf("Triggering Fixer Minion on branch %s...", branchName)
	fixerLogs := minion.RunFixer(issue.GetNumber(), branchName, alertName, diagnosis, telemetry)
	github.AddIssueComment(issue.GetNumber(), fixerLogs)

	log.Printf("Creating Pull Request...")
	prTitle := fmt.Sprintf("Fix for %s (Issue #%d)", alertName, issue.GetNumber())
	prBody := fmt.Sprintf("Automated PR by AI Fixer Minion.\nCloses #%d\n\n### Fixer Logs\n```text\n%s\n```", issue.GetNumber(), fixerLogs)
	
	pr, err := github.CreatePullRequest(branchName, prTitle, prBody)
	if err != nil {
		log.Printf("Error creating PR: %v", err)
		return
	}

	log.Printf("Fetching PR Diff for #%d...", pr.GetNumber())
	diff, _ := github.GetPullRequestDiff(pr.GetNumber())

	log.Printf("Triggering Reviewer Minion for PR #%d...", pr.GetNumber())
	approved, reviewComments := minion.RunReviewer(pr.GetNumber(), branchName, diff, diagnosis)

	if approved {
		log.Printf("PR #%d Approved! Merging...", pr.GetNumber())
		github.MergePullRequest(pr.GetNumber())
		github.AddIssueComment(pr.GetNumber(), "✅ **Reviewer Minion Approved**: The diff looks good and addresses the triage diagnosis. Merging automatically.")
	} else {
		log.Printf("PR #%d Rejected. Halting and assigning human...", pr.GetNumber())
		rejectionMsg := fmt.Sprintf("❌ **Reviewer Minion Rejected**: The fix is inadequate, unsafe, or introduces regressions.\n\n### Feedback\n```text\n%s\n```\n\nHalting AI automation and assigning a human for manual review.", reviewComments)
		github.AddIssueComment(pr.GetNumber(), rejectionMsg)
		github.AssignIssueOrPR(pr.GetNumber(), config.GithubOwner)
		github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner) // Assign original issue too
	}
}
