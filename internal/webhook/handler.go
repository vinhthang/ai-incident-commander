package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/db"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/incident"
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
	if config.WebhookAuthToken != "" && !isAuthorizedBearerToken(r.Header.Get("Authorization"), config.WebhookAuthToken) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

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
			_, _, fingerprint := computeAlertIdentity(aName, aLabels)
			wasActive := false
			lockAcquired := false
			if aStatus == "resolved" {
				wasActive = state.HasActiveIncident(fingerprint)
				lockAcquired = state.AcquireResolveLock(fingerprint)
			} else {
				lockAcquired = state.AcquireIncidentLock(fingerprint, aStatus)
			}
			if !lockAcquired {
				return // Dropped or debounced
			}
			// Enforce 10-minute overall lifecycle timeout to prevent leaked goroutines
			bgCtx, cancel := context.WithTimeout(detachedCtx, 10*time.Minute)
			defer cancel()

			processAlert(bgCtx, aStatus, aName, aLabels, aAnnotations, wasActive)
		}(context.WithoutCancel(ctx), alert.Status, alertName, alert.Labels, alert.Annotations)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Webhook received"))
}

// HandleHealthz reports liveness/readiness state including Postgres ping and AGY worker queue depth.
func HandleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dbStatus := "disabled"
	statusCode := http.StatusOK
	overallStatus := "ok"

	if db.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.DB.PingContext(ctx); err != nil {
			dbStatus = "error"
			statusCode = http.StatusServiceUnavailable
			overallStatus = "degraded"
		} else {
			dbStatus = "connected"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      overallStatus,
		"db":          dbStatus,
		"queue_depth": len(minion.AgyQueue),
	})
}

func isAuthorizedBearerToken(authHeader, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	provided := strings.TrimPrefix(authHeader, prefix)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func isApplicationErrorAlert(labels, annotations map[string]string) bool {
	if strings.EqualFold(strings.TrimSpace(labels["category"]), "application") {
		return true
	}
	for _, key := range []string{"trace_id", "status_code", "topic"} {
		if strings.TrimSpace(labels[key]) != "" || strings.TrimSpace(annotations[key]) != "" {
			return true
		}
	}
	return false
}

func computeAlertIdentity(alertName string, labels map[string]string) (namespace, target, fingerprint string) {
	namespace = incident.ExtractNamespace(labels)
	target = incident.ExtractTargetResource(labels)
	fingerprint = incident.ComputeFingerprint(alertName, namespace, target)
	return namespace, target, fingerprint
}

func processAlert(ctx context.Context, status, alertName string, labels, annotations map[string]string, wasActive bool) {
	namespace, target, fingerprint := computeAlertIdentity(alertName, labels)

	ctx, span := tracer.Start(ctx, "processAlert")
	defer span.End()
	defer state.ReleaseIncidentLock(fingerprint, "COOLING_DOWN")

	span.SetAttributes(
		attribute.String("alert.name", alertName),
		attribute.String("alert.status", status),
	)

	if status == "resolved" {
		log.Printf("Alert %s resolved. Cleaning active incident state.", alertName)
		span.AddEvent("Alert resolved. Tracking removed.")

		closedIssue, err := incident.ResolveClusterIncident(fingerprint)
		if err != nil {
			log.Printf("Error resolving cluster incident %s: %v", fingerprint, err)
		}
		if closedIssue > 0 {
			comment := fmt.Sprintf("✅ Alert %s (%s/%s) resolved: Cluster telemetry confirmed healthy.", alertName, namespace, target)
			if err := github.CloseIssue(closedIssue, comment); err != nil {
				log.Printf("Failed to close issue #%d: %v", closedIssue, err)
			}
		} else if wasActive {
			titlePrefix := fmt.Sprintf("[Alert] %s: %s/%s", alertName, namespace, target)
			issue, err := github.FindOpenIssueByTitle(titlePrefix)
			if err != nil {
				log.Printf("Failed searching open issue by title prefix %s: %v", titlePrefix, err)
			} else if issue != nil {
				comment := fmt.Sprintf("✅ Alert %s (%s/%s) resolved: Cluster telemetry confirmed healthy.", alertName, namespace, target)
				if err := github.CloseIssue(issue.GetNumber(), comment); err != nil {
					log.Printf("Failed to close found issue #%d: %v", issue.GetNumber(), err)
				}
			}
		}

		state.StateMu.Lock()
		delete(state.ActiveIncidents, fingerprint)
		state.StateMu.Unlock()
		return
	}

	if namespace == "apps" && isApplicationErrorAlert(labels, annotations) {
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

		// 1. Deduplicate against Postgres BEFORE calling Triage
		isDuplicate, err := minion.TrackAppIncident(serviceName, endpointOrTopic, statusCode, traceID)
		if err != nil {
			log.Printf("AppFixer tracking error: %v", err)
			span.RecordError(err)
		}
		if isDuplicate {
			span.AddEvent("App incident deduplicated by Postgres. Halting.")
			return
		}

		// 2. Fetch Telemetry and Run Triage for new incident
		span.AddEvent("Fetching Telemetry")
		telemetry := k8s.FetchTelemetry(labels)

		log.Printf("New app alert %s. Running Triage Minion...", alertName)
		span.AddEvent("Running Triage Minion")
		diagnosis := minion.RunTriage(ctx, alertName, labels, annotations, telemetry)

		state := "open"
		if diagnosis == "IGNORED" || minion.IsIgnored(diagnosis) {
			log.Printf("Triage Minion ignored app alert %s. Saving state as ignored.", alertName)
			span.AddEvent("Alert IGNORED by Triage Minion.")
			state = "ignored"
		}

		// 3. Save new incident to database
		err = minion.SaveNewAppIncident(serviceName, endpointOrTopic, statusCode, traceID, state, diagnosis)
		if err != nil {
			log.Printf("AppFixer save error: %v", err)
			span.RecordError(err)
		}

		return // Halt for apps, no GitHub PRs for now
	}

	if config.SoakDuration > 0 {
		span.AddEvent("Waiting Stabilization Soak")
		log.Printf("Alert %s (%s/%s) entering %s stabilization soak...", alertName, namespace, target, config.SoakDuration)
		if resolvedEarly := state.WaitForSoak(ctx, fingerprint, config.SoakDuration); resolvedEarly {
			log.Printf("Alert %s (%s/%s) resolved or cancelled during soak. Halting cleanly.", alertName, namespace, target)
			span.AddEvent("Resolved early during stabilization soak")
			state.StateMu.Lock()
			delete(state.ActiveIncidents, fingerprint)
			state.StateMu.Unlock()
			return
		}
	}

	if ws := k8s.CheckWorkloadStatus(labels); ws.InProgress || ws.Recovered {
		log.Printf("Deferring/suppressing alert %s (%s/%s): %s", alertName, namespace, target, ws.Summary)
		span.AddEvent(fmt.Sprintf("Pre-triage workload check skipped action: %s", ws.Summary))
		state.StateMu.Lock()
		delete(state.ActiveIncidents, fingerprint)
		state.StateMu.Unlock()
		return
	}

	span.AddEvent("Fetching Diagnostic Bundle (Service & Helm)")
	telemetry := k8s.FetchDiagnosticBundle(labels, config.WorkspaceDir)

	if incident.IsEphemeralNodeOutage(labels, telemetry) {
		log.Printf("Alert %s on ephemeral node %s suppressed...", alertName, labels["node"])
		span.AddEvent("Alert suppressed due to ephemeral node outage.")
		return
	}

	isDuplicate, existingIssue, err := incident.TrackClusterIncident(fingerprint, alertName, namespace, target)
	if err != nil {
		log.Printf("Error tracking cluster incident %s: %v", fingerprint, err)
	}
	if isDuplicate {
		log.Printf("Duplicate alert %s detected (open in issue #%d). Incrementing count and skipping.", alertName, existingIssue)
		span.AddEvent(fmt.Sprintf("Duplicate alert detected. Issue #%d already open.", existingIssue))
		return
	}

	log.Printf("New alert %s. Running Diagnostic Triage Minion...", alertName)
	span.AddEvent("Running Diagnostic Triage Minion")
	triageRes := minion.RunDiagnosticTriage(ctx, alertName, labels, annotations, telemetry)

	if triageRes.Err != nil {
		log.Printf("Diagnostic Triage failed for %s (%s/%s): %v. Halting safely without action.", alertName, namespace, target, triageRes.Err)
		span.RecordError(triageRes.Err)
		return
	}

	if triageRes.Action == minion.ActionIgnore {
		log.Printf("Diagnostic Triage ignored alert %s (confidence=%s). Halting.", alertName, triageRes.Confidence)
		span.AddEvent("Alert IGNORED by Diagnostic Triage Minion.")
		_ = incident.RecordClusterIssue(fingerprint, alertName, namespace, target, 0, "ignored")
		return
	}

	issueLabels := []string{"ai-incident"}
	if triageRes.Action == minion.ActionEscalateHuman {
		issueLabels = append(issueLabels, "needs-human-review")
	}

	span.AddEvent("Creating GitHub Issue")
	issueTitle := fmt.Sprintf("[Alert] %s: %s/%s", alertName, namespace, target)
	issueBody := fmt.Sprintf("## Grafana Alert: %s\n\n**Verdict**: `%s` (Confidence: `%s`)\n\n%s\n\n<details>\n<summary>Collected Diagnostic Telemetry (Service + Helm)</summary>\n\n```text\n%s\n```\n</details>",
		alertName, triageRes.Action, triageRes.Confidence, triageRes.Summary, telemetry)

	issue, err := github.CreateIssue(issueTitle, issueBody, issueLabels)
	if err != nil {
		log.Printf("Error creating issue: %v", err)
		span.RecordError(err)
		return
	}
	span.SetAttributes(attribute.Int("github.issue", issue.GetNumber()))

	_ = incident.RecordClusterIssue(fingerprint, alertName, namespace, target, issue.GetNumber(), "firing")

	if triageRes.Action == minion.ActionEscalateHuman {
		log.Printf("Diagnostic Triage concluded ESCALATE_HUMAN (confidence=%s) for issue #%d. Skipping Fixer/PR.", triageRes.Confidence, issue.GetNumber())
		_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner)
		return
	}

	state.StateMu.Lock()
	state.ActiveIncidents[fingerprint] = &state.IncidentState{
		IssueNumber:  issue.GetNumber(),
		AttemptCount: 0,
		LastAttempt:  time.Now(),
		Status:       "FIXING",
	}
	state.StateMu.Unlock()

	remediateClusterIssue(ctx, span, issue.GetNumber(), alertName, triageRes.Summary, telemetry)
}

func remediateClusterIssue(ctx context.Context, span trace.Span, issueNumber int, alertName, diagnosis, telemetry string) {
	workspace.Lock()
	defer workspace.Unlock()

	log.Printf("Created issue #%d. Creating branch...", issueNumber)
	span.AddEvent("Resetting Workspace")
	if err := workspace.ResetToMain(); err != nil {
		log.Printf("Failed resetting workspace to main: %v", err)
		span.RecordError(err)
		_ = github.AssignIssueOrPR(issueNumber, config.GithubOwner)
		return
	}

	branchName := fmt.Sprintf("fix/alert-%d-%d", issueNumber, time.Now().Unix())
	span.SetAttributes(attribute.String("git.branch", branchName))

	if err := workspace.CreateAndCheckoutBranch(branchName); err != nil {
		log.Printf("Failed to create branch: %v", err)
		span.RecordError(err)
		_ = github.AssignIssueOrPR(issueNumber, config.GithubOwner)
		return
	}

	var reviewFeedback string
	prCreated := false
	var prNumber int

	for attempt := 1; attempt <= 3; attempt++ {
		log.Printf("Triggering Fixer Minion on branch %s (Attempt %d)...", branchName, attempt)
		span.AddEvent(fmt.Sprintf("Running Fixer Minion (Attempt %d)", attempt))

		fixerLogs := minion.RunFixer(ctx, issueNumber, branchName, alertName, diagnosis, telemetry, reviewFeedback)
		_ = github.AddIssueComment(issueNumber, fmt.Sprintf("Attempt %d:\n%s", attempt, fixerLogs))

		span.AddEvent("Fetching Local Diff")
		localDiff, err := workspace.GetLocalDiff("main")
		if err != nil {
			log.Printf("Failed to get local diff: %v", err)
			break
		}

		log.Printf("Triggering Senior Fixer Minion...")
		span.AddEvent("Running Senior Fixer")
		sfApproved, sfComments := minion.RunSeniorFixer(ctx, issueNumber, branchName, diagnosis, localDiff)

		if !sfApproved {
			log.Printf("Senior Fixer rejected. Retrying Fixer...")
			span.AddEvent("Senior Fixer Rejected")
			reviewFeedback = sfComments
			continue
		}

		if !prCreated {
			log.Printf("Creating Pull Request...")
			span.AddEvent("Creating Pull Request")
			prTitle := fmt.Sprintf("Fix for %s (Issue #%d)", alertName, issueNumber)
			prBody := fmt.Sprintf("Automated PR by AI Fixer Minion.\nCloses #%d", issueNumber)

			pr, err := github.CreatePullRequest(branchName, prTitle, prBody)
			if err != nil {
				log.Printf("Error creating PR: %v", err)
				break
			}
			prCreated = true
			prNumber = pr.GetNumber()
			span.SetAttributes(attribute.Int("github.pr", prNumber))
		}

		log.Printf("Fetching PR Diff for #%d...", prNumber)
		diff, err := github.GetPullRequestDiff(prNumber)
		if err != nil {
			log.Printf("Error fetching PR diff for #%d: %v", prNumber, err)
			break
		}

		log.Printf("Triggering Reviewer Minion for PR #%d...", prNumber)
		approved, reviewComments := minion.RunReviewer(ctx, prNumber, branchName, diff, diagnosis)

		if approved {
			log.Printf("PR #%d Approved by Reviewer! Waiting for CI check runs before merge...", prNumber)
			ciPassed, ciSummary, ciErr := github.WaitForPRChecks(ctx, prNumber, 15*time.Second, 5*time.Minute)
			if ciErr != nil {
				log.Printf("Timed out or failed polling CI checks for PR #%d: %v", prNumber, ciErr)
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("⚠️ **Reviewer Approved**, but waiting for CI checks failed (%v). Please verify CI and merge manually.", ciErr))
				_ = github.AssignIssueOrPR(prNumber, config.GithubOwner)
				return
			}
			if !ciPassed {
				log.Printf("CI checks failed on PR #%d (%s). Feeding back to Fixer...", prNumber, ciSummary)
				reviewFeedback = ciSummary
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("⚠️ **Reviewer Approved**, but CI checks failed:\n\n```text\n%s\n```\nRetrying Fixer...", ciSummary))
				continue
			}

			if !config.AutoMerge {
				log.Printf("PR #%d Approved & CI Passed (%s); assigning human for merge (AUTO_MERGE=false).", prNumber, ciSummary)
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("✅ **Diagnostic, Reviewer & CI Passed** (%s): Ready for human merge (`AUTO_MERGE=false`).", ciSummary))
				_ = github.AssignIssueOrPR(prNumber, config.GithubOwner)
				return
			}

			log.Printf("PR #%d Approved & CI Passed (%s)! Merging...", prNumber, ciSummary)
			if mergeErr := github.MergePullRequest(prNumber); mergeErr != nil {
				log.Printf("Failed to auto-merge PR #%d: %v", prNumber, mergeErr)
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("⚠️ **Reviewer & CI Approved**, but auto-merge failed: %v. Please merge manually.", mergeErr))
				_ = github.AssignIssueOrPR(prNumber, config.GithubOwner)
			} else {
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("✅ **Reviewer Minion & CI Approved** (%s): The diff looks good and CI passed. Merged automatically.", ciSummary))
			}
			return
		} else {
			log.Printf("PR #%d Rejected. Feeding back to Fixer...", prNumber)
			reviewFeedback = reviewComments
			_ = github.AddIssueComment(prNumber, fmt.Sprintf("❌ **Reviewer Minion Rejected**: Attempting to fix again.\n\n```text\n%s\n```", reviewComments))
		}
	}

	log.Printf("Exhausted automated fix attempts. Assigning human...")
	if prCreated {
		_ = github.AddIssueComment(prNumber, "❌ **AI Automation Exhausted**: Maximum retry attempts reached. Assigning a human.")
		_ = github.AssignIssueOrPR(prNumber, config.GithubOwner)
	}
	_ = github.AssignIssueOrPR(issueNumber, config.GithubOwner)
}
