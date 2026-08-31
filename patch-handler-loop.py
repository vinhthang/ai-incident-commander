import re

with open('internal/webhook/handler.go', 'r') as f:
    content = f.read()

old_loop_logic = """	log.Printf("Triggering Fixer Minion on branch %s...", branchName)
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
}"""

new_loop_logic = """	var reviewFeedback string
	prCreated := false
	var prNumber int

	for attempt := 1; attempt <= 3; attempt++ {
		log.Printf("Triggering Fixer Minion on branch %s (Attempt %d)...", branchName, attempt)
		span.AddEvent(fmt.Sprintf("Running Fixer Minion (Attempt %d)", attempt))
		
		fixerLogs := minion.RunFixer(ctx, issue.GetNumber(), branchName, alertName, diagnosis, telemetry, reviewFeedback)
		_ = github.AddIssueComment(issue.GetNumber(), fmt.Sprintf("Attempt %d:\n%s", attempt, fixerLogs))

		span.AddEvent("Fetching Local Diff")
		localDiff, err := workspace.GetLocalDiff("main")
		if err != nil {
			log.Printf("Failed to get local diff: %v", err)
			break
		}

		log.Printf("Triggering Senior Fixer Minion...")
		span.AddEvent("Running Senior Fixer")
		sfApproved, sfComments := minion.RunSeniorFixer(ctx, issue.GetNumber(), branchName, diagnosis, localDiff)
		
		if !sfApproved {
			log.Printf("Senior Fixer rejected. Retrying Fixer...")
			span.AddEvent("Senior Fixer Rejected")
			reviewFeedback = sfComments
			continue
		}

		if !prCreated {
			log.Printf("Creating Pull Request...")
			span.AddEvent("Creating Pull Request")
			prTitle := fmt.Sprintf("Fix for %s (Issue #%d)", alertName, issue.GetNumber())
			prBody := fmt.Sprintf("Automated PR by AI Fixer Minion.\nCloses #%d", issue.GetNumber())

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
			log.Printf("PR #%d Approved! Merging...", prNumber)
			if mergeErr := github.MergePullRequest(prNumber); mergeErr != nil {
				log.Printf("Failed to auto-merge PR #%d: %v", prNumber, mergeErr)
				_ = github.AddIssueComment(prNumber, fmt.Sprintf("⚠️ **Reviewer Approved**, but auto-merge failed: %v. Please merge manually.", mergeErr))
				_ = github.AssignIssueOrPR(prNumber, config.GithubOwner)
			} else {
				_ = github.AddIssueComment(prNumber, "✅ **Reviewer Minion Approved**: The diff looks good and addresses the triage diagnosis. Merged automatically.")
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
	_ = github.AssignIssueOrPR(issue.GetNumber(), config.GithubOwner)
}"""

content = content.replace(old_loop_logic, new_loop_logic)

with open('internal/webhook/handler.go', 'w') as f:
    f.write(content)
