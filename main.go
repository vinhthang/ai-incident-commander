package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v59/github"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

var (
	githubToken  = os.Getenv("GITHUB_TOKEN")
	geminiAPIKey = os.Getenv("GEMINI_API_KEY")
	geminiModel  = getEnv("GEMINI_MODEL", "gemini-3.5-flash")
	listenPort   = getEnv("PORT", "8085")
	githubOwner  = getEnv("GITHUB_OWNER", "vinhthang")
	githubRepo   = getEnv("GITHUB_REPO", "oci")

	workspaceDir = "/app/workspace/oci"
	cooldownMins = 5 * time.Minute
)

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

type GrafanaAlertPayload struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"alerts"`
}

type IncidentState struct {
	IssueNumber  int
	AttemptCount int
	LastAttempt  time.Time
	Status       string // "TRIAGING", "FIXING", "COOLING_DOWN"
}

var (
	geminiLimiter   = rate.NewLimiter(rate.Every(21*time.Second), 1)
	activeIncidents = make(map[string]*IncidentState)
	stateMu         sync.RWMutex
)

func main() {
	if githubToken == "" || geminiAPIKey == "" {
		log.Println("WARNING: GITHUB_TOKEN or GEMINI_API_KEY is not set. API calls will fail.")
	}

	initWorkspace()

	http.HandleFunc("/webhook", handleWebhook)
	log.Printf("🚀 AI Incident Commander starting on :%s (model: %s)", listenPort, geminiModel)
	log.Fatal(http.ListenAndServe(":"+listenPort, nil))
}

func initWorkspace() {
	// Configure git for HTTPS cloning with token
	exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").Run()
	exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").Run()
	if githubToken != "" {
		cmd := exec.Command("sh", "-c", fmt.Sprintf("git config --global url.\"https://oauth2:%s@github.com/\".insteadOf \"https://github.com/\"", githubToken))
		cmd.Run()
	}

	os.MkdirAll("/app/workspace", 0755)
	if _, err := os.Stat(workspaceDir); os.IsNotExist(err) {
		log.Printf("Cloning %s/%s into %s", githubOwner, githubRepo, workspaceDir)
		cmd := exec.Command("git", "clone", fmt.Sprintf("https://github.com/%s/%s.git", githubOwner, githubRepo), workspaceDir)
		cmd.Dir = "/app/workspace"
		if err := cmd.Run(); err != nil {
			log.Printf("WARNING: Failed to clone workspace: %v", err)
		}
	} else {
		log.Printf("Workspace already exists at %s. Pulling latest.", workspaceDir)
		cmd := exec.Command("git", "pull")
		cmd.Dir = workspaceDir
		cmd.Run()
	}
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
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
			if !acquireIncidentLock(aName, aStatus) {
				return // Dropped or debounced
			}
			processAlert(aStatus, aName, aLabels, aAnnotations)
		}(alert.Status, alertName, alert.Labels, alert.Annotations)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook received"))
}

func acquireIncidentLock(alertName, status string) bool {
	stateMu.Lock()
	defer stateMu.Unlock()

	state, exists := activeIncidents[alertName]
	if !exists {
		if status == "resolved" {
			return false // Ignore resolved for non-existing incidents
		}
		activeIncidents[alertName] = &IncidentState{
			AttemptCount: 0,
			LastAttempt:  time.Now(),
			Status:       "TRIAGING",
		}
		return true
	}

	if status == "resolved" {
		state.Status = "RESOLVING"
		return true
	}

	// It's firing and already tracked. Check cooldown.
	if state.Status == "TRIAGING" || state.Status == "FIXING" {
		log.Printf("Alert %s is currently %s. Dropping concurrent webhook.", alertName, state.Status)
		return false
	}

	if time.Since(state.LastAttempt) < cooldownMins {
		log.Printf("Alert %s is within %v cooldown. Dropping webhook.", alertName, cooldownMins)
		return false
	}

	state.Status = "TRIAGING"
	state.LastAttempt = time.Now()
	return true
}

func releaseIncidentLock(alertName string, newStatus string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	if state, exists := activeIncidents[alertName]; exists {
		state.Status = newStatus
	}
}

func fetchTelemetry(labels map[string]string) string {
	namespace := labels["namespace"]
	pod := labels["pod"]
	if pod == "" {
		pod = labels["instance"] // Sometimes instance contains pod name
	}

	if namespace == "" || pod == "" {
		return "No namespace or pod specified in labels to fetch telemetry."
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Telemetry for Pod %s in Namespace %s:\n\n", pod, namespace))

	// Fetch Logs
	logCmd := exec.Command("kubectl", "logs", "-n", namespace, pod, "--tail=50")
	logOutput, err := logCmd.CombinedOutput()
	if err == nil {
		builder.WriteString("=== Recent Logs ===\n")
		builder.WriteString(string(logOutput) + "\n\n")
	}

	// Fetch Events
	evtCmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--field-selector", "involvedObject.name="+pod)
	evtOutput, err := evtCmd.CombinedOutput()
	if err == nil {
		builder.WriteString("=== Recent Events ===\n")
		builder.WriteString(string(evtOutput) + "\n\n")
	}

	return builder.String()
}

func processAlert(status, alertName string, labels, annotations map[string]string) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	issueTitle := fmt.Sprintf("[Alert] %s", alertName)

	opts := &github.IssueListByRepoOptions{State: "open"}
	issues, _, err := client.Issues.ListByRepo(ctx, githubOwner, githubRepo, opts)
	if err != nil {
		log.Printf("Error fetching issues: %v", err)
		releaseIncidentLock(alertName, "COOLING_DOWN")
		return
	}

	var existingIssue *github.Issue
	for _, issue := range issues {
		if strings.Contains(*issue.Title, issueTitle) {
			existingIssue = issue
			break
		}
	}

	// Update our state's IssueNumber if found
	stateMu.Lock()
	state := activeIncidents[alertName]
	if existingIssue != nil {
		state.IssueNumber = existingIssue.GetNumber()
		
		// Resync attempt count from labels
		state.AttemptCount = 0
		for _, label := range existingIssue.Labels {
			if strings.HasPrefix(label.GetName(), "attempt:") {
				fmt.Sscanf(label.GetName(), "attempt:%d", &state.AttemptCount)
			}
		}
	}
	stateMu.Unlock()

	if status == "resolved" {
		if existingIssue != nil {
			log.Printf("Alert %s resolved. Closing issue.", alertName)
			closedStr := "closed"
			client.Issues.Edit(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), &github.IssueRequest{State: &closedStr})
			comment := &github.IssueComment{Body: github.String("✅ Alert resolved. Incident commander has closed this issue.")}
			client.Issues.CreateComment(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), comment)
		}
		stateMu.Lock()
		delete(activeIncidents, alertName)
		stateMu.Unlock()
		return
	}

	telemetry := fetchTelemetry(labels)

	if existingIssue == nil {
		log.Printf("New alert %s. Running Triage Minion...", alertName)
		diagnosis := runTriageMinion(alertName, labels, annotations, telemetry)

		stateMu.Lock()
		state.AttemptCount = 1
		stateMu.Unlock()

		issueReq := &github.IssueRequest{
			Title:  github.String(issueTitle),
			Body:   github.String(fmt.Sprintf("## Grafana Alert: %s\n\n**Diagnosis from Triage Minion:**\n%s\n\n**Telemetry:**\n```text\n%s\n```", alertName, diagnosis, telemetry)),
			Labels: &[]string{"ai-incident", "attempt:1"},
		}
		newIssue, _, err := client.Issues.Create(ctx, githubOwner, githubRepo, issueReq)
		if err != nil {
			log.Printf("Error creating issue: %v", err)
			releaseIncidentLock(alertName, "COOLING_DOWN")
			return
		}
		log.Printf("Created issue #%d", newIssue.GetNumber())
		
		stateMu.Lock()
		state.IssueNumber = newIssue.GetNumber()
		state.Status = "FIXING"
		stateMu.Unlock()

		log.Printf("Triggering Fixer Minion for issue #%d", newIssue.GetNumber())
		runFixerMinion(newIssue.GetNumber(), alertName, 1, diagnosis, telemetry, client)
	} else {
		if state.AttemptCount >= 3 {
			log.Printf("Issue #%d reached max attempts (3). Triggering Rollback...", existingIssue.GetNumber())

			hasFailedLabel := false
			for _, label := range existingIssue.Labels {
				if label.GetName() == "failed-fix" {
					hasFailedLabel = true
				}
			}
			if !hasFailedLabel {
				stateMu.Lock()
				state.Status = "FIXING"
				stateMu.Unlock()

				runRollbackMinion(existingIssue.GetNumber(), client)

				newLabels := []string{"ai-incident", "failed-fix"}
				client.Issues.Edit(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), &github.IssueRequest{Labels: &newLabels})
			}
		} else {
			stateMu.Lock()
			state.AttemptCount++
			state.Status = "FIXING"
			attemptCount := state.AttemptCount
			stateMu.Unlock()

			log.Printf("Triggering Fixer Minion for issue #%d (Attempt %d)", existingIssue.GetNumber(), attemptCount)
			
			// Get fresh diagnosis
			diagnosis := runTriageMinion(alertName, labels, annotations, telemetry)
			runFixerMinion(existingIssue.GetNumber(), alertName, attemptCount, diagnosis, telemetry, client)

			newLabel := fmt.Sprintf("attempt:%d", attemptCount)
			var labelStrings []string
			for _, l := range existingIssue.Labels {
				if !strings.HasPrefix(l.GetName(), "attempt:") {
					labelStrings = append(labelStrings, l.GetName())
				}
			}
			labelStrings = append(labelStrings, newLabel)
			client.Issues.Edit(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), &github.IssueRequest{Labels: &labelStrings})
		}
	}
	
	releaseIncidentLock(alertName, "COOLING_DOWN")
}

func runTriageMinion(alertName string, labels, annotations map[string]string, telemetry string) string {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", geminiModel, geminiAPIKey)
	prompt := fmt.Sprintf("You are the Triage Minion for a Kubernetes GitOps cluster. Analyze this Grafana Alert and the provided telemetry to formulate a brief root cause hypothesis and suggested fix action.\n\nAlert: %s\nLabels: %v\nAnnotations: %v\nTelemetry:\n%s", alertName, labels, annotations, telemetry)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]interface{}{{"text": prompt}}},
		},
	})

	geminiLimiter.Wait(context.Background())
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Gemini triage request error: %v", err)
		return "⚠️ Failed to contact Gemini API for triage. Attempting blind fix."
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Gemini triage API returned HTTP %d: %s", resp.StatusCode, string(bodyBytes))
		return "⚠️ Failed to contact Gemini API for triage. Attempting blind fix."
	}

	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)

	if candidates, ok := res["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if content, ok := candidates[0].(map[string]interface{})["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
				if text, ok := parts[0].(map[string]interface{})["text"].(string); ok {
					return text
				}
			}
		}
	}
	return "Analyzed alert, proceeding with Fixer."
}

func runFixerMinion(issueNumber int, alertName string, attempt int, diagnosis, telemetry string, client *github.Client) {
	prompt := fmt.Sprintf("You are the Fixer Minion. GitHub Issue #%d reports an alert '%s'. This is attempt %d.\n\nTriage Diagnosis:\n%s\n\nTelemetry:\n%s\n\nAnalyze the repository locally, determine the root cause, apply the required configuration or code fix, commit, and push directly to main.", issueNumber, alertName, attempt, diagnosis, telemetry)
	
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = workspaceDir
	
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	
	var executionOutput string
	if err != nil {
		log.Printf("Fixer Minion failed to run: %v", err)
		executionOutput = fmt.Sprintf("❌ Fixer Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", err, outBuf.String(), errBuf.String())
	} else {
		executionOutput = fmt.Sprintf("✅ Fixer Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```", outBuf.String())
	}

	ctx := context.Background()
	comment := &github.IssueComment{Body: github.String(executionOutput)}
	client.Issues.CreateComment(ctx, githubOwner, githubRepo, issueNumber, comment)
}

func runRollbackMinion(issueNumber int, client *github.Client) {
	prompt := fmt.Sprintf("You are the Rollback Minion. Issue #%d failed after 3 attempts. Run git revert HEAD, commit, and push to rollback the bad fixes.", issueNumber)
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = workspaceDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	
	var executionOutput string
	if err != nil {
		log.Printf("Rollback Minion failed to run: %v", err)
		executionOutput = fmt.Sprintf("❌ Rollback Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", err, outBuf.String(), errBuf.String())
	} else {
		executionOutput = fmt.Sprintf("⏪ Rollback Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```\n\nAwaiting manual intervention.", outBuf.String())
	}

	ctx := context.Background()
	comment := &github.IssueComment{Body: github.String(executionOutput)}
	client.Issues.CreateComment(ctx, githubOwner, githubRepo, issueNumber, comment)
}
