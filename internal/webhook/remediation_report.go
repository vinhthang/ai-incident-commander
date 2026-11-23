package webhook

import (
	"context"
	"fmt"
	"log"
	"strings"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/db"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/incident"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/metrics"
	"vinhthang.dev/ai-incident-commander/internal/state"
)

// RemediationReport summarizes the outcome of a reactive or proactive auto-remediation run.
type RemediationReport struct {
	Source      string
	Mode        string
	Target      string
	Outcome     string
	RootCause   string
	Details     string
	Fingerprint string
	IssueNumber int
	PRNumber    int
}

var (
	memosInsertFn         = db.InsertMemosReport
	runtimeActionExecutor = k8s.ExecuteRuntimeAction
	clusterResolveFn      = incident.ResolveClusterIncident
)

func reportRemediationOutcome(ctx context.Context, rep RemediationReport) {
	if strings.TrimSpace(config.MemosDBDSN) == "" {
		return
	}

	var issueRef, prRef string
	if rep.IssueNumber > 0 {
		issueRef = fmt.Sprintf("#%d", rep.IssueNumber)
	}
	if rep.PRNumber > 0 {
		prRef = fmt.Sprintf("#%d", rep.PRNumber)
	}

	note := db.FormatRemediationMemosNote(
		rep.Source,
		rep.Mode,
		rep.Target,
		rep.Outcome,
		rep.RootCause,
		rep.Details,
		issueRef,
		prRef,
	)
	if err := memosInsertFn(ctx, config.MemosDBDSN, note, false); err != nil {
		log.Printf("⚠️ Failed inserting auto-remediation note into Memos: %v", err)
	}
}

func executeAndReportRuntimeFix(ctx context.Context, source, fingerprint string, issueNumber int, act k8s.RuntimeAction, rootCause string) k8s.RuntimeActionResult {
	res := runtimeActionExecutor(ctx, act)
	target := act.TargetString()

	if res.Verified {
		outcome := "runtime_verified"
		if fingerprint != "" {
			if _, err := clusterResolveFn(fingerprint); err != nil {
				log.Printf("⚠️ Failed resolving cluster incident %s in DB: %v", fingerprint, err)
			}
			state.StateMu.Lock()
			delete(state.ActiveIncidents, fingerprint)
			state.StateMu.Unlock()
		}

		verifyText := strings.TrimSpace(res.VerifyOutput)
		if verifyText == "" {
			verifyText = strings.TrimSpace(res.Output)
		}
		details := strings.TrimSpace(strings.TrimSpace(res.Output) + "\n" + strings.TrimSpace(res.VerifyOutput))

		if issueNumber > 0 && github.Client != nil {
			comment := fmt.Sprintf("✅ **Auto-Remediation Verified** (`%s` on `%s`)\n\n%s\n\n<details>\n<summary>Verification Output</summary>\n\n```text\n%s\n```\n</details>",
				act.Type, target, strings.TrimSpace(rootCause), verifyText)
			if err := github.CloseIssue(ctx, issueNumber, comment); err != nil {
				log.Printf("⚠️ Failed closing verified runtime issue #%d: %v", issueNumber, err)
			}
		}

		metrics.IncRemediationOutcome(outcome)
		reportRemediationOutcome(ctx, RemediationReport{
			Source:      source,
			Mode:        "runtime",
			Target:      target,
			Outcome:     outcome,
			RootCause:   rootCause,
			Details:     details,
			Fingerprint: fingerprint,
			IssueNumber: issueNumber,
		})
		return res
	}

	outcome := "runtime_failed"
	if res.RBACDenied {
		outcome = "runtime_rbac_denied"
	}

	detailParts := make([]string, 0, 4)
	if res.RBACDenied {
		detailParts = append(detailParts, "Kubernetes RBAC denied runtime mutation (Forbidden). Update the ClusterRole in `vinhthang/oci` to enable live runtime actions.")
	}
	if res.DeniedReason != "" {
		detailParts = append(detailParts, "Denied: "+res.DeniedReason)
	}
	if res.Err != nil {
		detailParts = append(detailParts, "Error: "+res.Err.Error())
	}
	if strings.TrimSpace(res.Output) != "" {
		detailParts = append(detailParts, "Command output:\n"+strings.TrimSpace(res.Output))
	}
	if strings.TrimSpace(res.VerifyOutput) != "" {
		detailParts = append(detailParts, "Verification output:\n"+strings.TrimSpace(res.VerifyOutput))
	}
	details := strings.Join(detailParts, "\n")

	if issueNumber > 0 && github.Client != nil {
		comment := fmt.Sprintf("❌ **Auto-Remediation Unsuccessful** (`%s` on `%s`, outcome: `%s`)\n\n```text\n%s\n```",
			act.Type, target, outcome, details)
		_ = github.AddIssueComment(ctx, issueNumber, comment)
		_ = github.AssignIssueOrPR(ctx, issueNumber, config.GithubOwner)
	}

	metrics.IncRemediationOutcome(outcome)
	reportRemediationOutcome(ctx, RemediationReport{
		Source:      source,
		Mode:        "runtime",
		Target:      target,
		Outcome:     outcome,
		RootCause:   rootCause,
		Details:     details,
		Fingerprint: fingerprint,
		IssueNumber: issueNumber,
	})
	return res
}
