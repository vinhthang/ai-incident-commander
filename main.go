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
	"golang.org/x/time/rate"
	"time"

	"github.com/google/go-github/v59/github"
	"golang.org/x/oauth2"
)

var (
	githubToken  = os.Getenv("GITHUB_TOKEN")
	geminiAPIKey = os.Getenv("GEMINI_API_KEY")
	geminiModel  = getEnv("GEMINI_MODEL", "gemini-3.5-flash")
	listenPort   = getEnv("PORT", "8085")
	githubOwner  = getEnv("GITHUB_OWNER", "vinhthang")
	githubRepo   = getEnv("GITHUB_REPO", "oci")
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

var geminiLimiter = rate.NewLimiter(rate.Every(21*time.Second), 1)

func main() {
	if githubToken == "" || geminiAPIKey == "" {
		log.Println("WARNING: GITHUB_TOKEN or GEMINI_API_KEY is not set. API calls will fail.")
	}

	http.HandleFunc("/webhook", handleWebhook)
	log.Printf("🚀 AI Incident Commander starting on :%s (model: %s)", listenPort, geminiModel)
	log.Fatal(http.ListenAndServe(":"+listenPort, nil))
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

		go processAlert(alert.Status, alertName, alert.Labels, alert.Annotations)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Webhook received"))
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
		return
	}

	var existingIssue *github.Issue
	for _, issue := range issues {
		if strings.Contains(*issue.Title, issueTitle) {
			existingIssue = issue
			break
		}
	}

	if status == "resolved" {
		if existingIssue != nil {
			log.Printf("Alert %s resolved. Running Verifier Minion...", alertName)
			runVerifierMinion(existingIssue.GetNumber())

			closedStr := "closed"
			client.Issues.Edit(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), &github.IssueRequest{State: &closedStr})

			comment := &github.IssueComment{Body: github.String("✅ Alert resolved. Verifier minion has verified the system health and closed this issue.")}
			client.Issues.CreateComment(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), comment)
		}
		return
	}

	if existingIssue == nil {
		log.Printf("New alert %s. Running Triage Minion...", alertName)
		diagnosis := runTriageMinion(alertName, labels, annotations)

		issueReq := &github.IssueRequest{
			Title:  github.String(issueTitle),
			Body:   github.String(fmt.Sprintf("## Grafana Alert: %s\n\n**Diagnosis from Triage Minion:**\n%s", alertName, diagnosis)),
			Labels: &[]string{"ai-incident", "attempt:1"},
		}
		newIssue, _, err := client.Issues.Create(ctx, githubOwner, githubRepo, issueReq)
		if err != nil {
			log.Printf("Error creating issue: %v", err)
			return
		}
		log.Printf("Created issue #%d", newIssue.GetNumber())

		log.Printf("Triggering Fixer Minion for issue #%d", newIssue.GetNumber())
		runFixerMinion(newIssue.GetNumber(), alertName, 1)
	} else {
		attemptCount := 1
		for _, label := range existingIssue.Labels {
			if strings.HasPrefix(label.GetName(), "attempt:") {
				fmt.Sscanf(label.GetName(), "attempt:%d", &attemptCount)
			}
		}

		if attemptCount >= 3 {
			log.Printf("Issue #%d reached max attempts (3). Triggering Rollback...", existingIssue.GetNumber())

			hasFailedLabel := false
			for _, label := range existingIssue.Labels {
				if label.GetName() == "failed-fix" {
					hasFailedLabel = true
				}
			}
			if !hasFailedLabel {
				runRollbackMinion(existingIssue.GetNumber())

				labels := []string{"ai-incident", "failed-fix"}
				client.Issues.Edit(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), &github.IssueRequest{Labels: &labels})
				comment := &github.IssueComment{Body: github.String("❌ Fixer minion failed 3 times. Git rollback initiated. Awaiting manual intervention.")}
				client.Issues.CreateComment(ctx, githubOwner, githubRepo, existingIssue.GetNumber(), comment)
			}
		} else {
			attemptCount++
			log.Printf("Triggering Fixer Minion for issue #%d (Attempt %d)", existingIssue.GetNumber(), attemptCount)
			runFixerMinion(existingIssue.GetNumber(), alertName, attemptCount)

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
}

func runTriageMinion(alertName string, labels, annotations map[string]string) string {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", geminiModel, geminiAPIKey)
	prompt := fmt.Sprintf("You are the Triage Minion for a Kubernetes GitOps cluster. Analyze this Grafana Alert and provide a brief root cause hypothesis and suggested action.\nAlert: %s\nLabels: %v\nAnnotations: %v", alertName, labels, annotations)

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

func runFixerMinion(issueNumber int, alertName string, attempt int) {
	// Configure git to use the GitHub token for HTTPS authentication
	exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").Run()
	exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").Run()
	exec.Command("sh", "-c", fmt.Sprintf("git config --global url.\"https://oauth2:%s@github.com/\".insteadOf \"https://github.com/\"", githubToken)).Run()

	prompt := fmt.Sprintf("You are the Fixer Minion. GitHub Issue #%d reports an alert '%s'. This is attempt %d. Clone the vinhthang/oci repository, edit the Helm chart values.yaml to fix the issue, commit, and push. You MUST post a summary of your actions and any push errors as a comment on Issue #%d.", issueNumber, alertName, attempt, issueNumber)
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("Fixer Minion failed to run: %v", err)
	}
}

func runRollbackMinion(issueNumber int) {
	exec.Command("git", "config", "--global", "user.email", "ai-incident-commander@vinhthang.dev").Run()
	exec.Command("git", "config", "--global", "user.name", "AI Incident Commander").Run()
	exec.Command("sh", "-c", fmt.Sprintf("git config --global url.\"https://oauth2:%s@github.com/\".insteadOf \"https://github.com/\"", githubToken)).Run()

	prompt := fmt.Sprintf("You are the Rollback Minion. Issue #%d failed after 3 attempts. Clone vinhthang/oci, run git revert HEAD, commit, and push to rollback the bad fixes. You MUST post a summary of your actions and any push errors as a comment on Issue #%d.", issueNumber, issueNumber)
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("Fixer Minion failed to run: %v", err)
	}
}

func runVerifierMinion(issueNumber int) {
	prompt := fmt.Sprintf("You are the Verifier Minion. The alert for Issue #%d is resolved. Briefly summarize the system health.", issueNumber)
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("Fixer Minion failed to run: %v", err)
	}
}
