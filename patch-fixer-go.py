with open('internal/minion/fixer.go', 'r') as f:
    content = f.read()

old_sig = "func RunFixer(ctx context.Context, issueNumber int, branchName, alertName, diagnosis, telemetry string) string {"
new_sig = "func RunFixer(ctx context.Context, issueNumber int, branchName, alertName, diagnosis, telemetry, reviewFeedback string) string {"

old_data = """	p, err := prompt.RenderFixerPrompt(prompt.FixerData{
		IssueNumber: issueNumber,
		BranchName:  branchName,
		AlertName:   alertName,
		Diagnosis:   diagnosis,
		Telemetry:   telemetry,
	})"""

new_data = """	p, err := prompt.RenderFixerPrompt(prompt.FixerData{
		IssueNumber:    issueNumber,
		BranchName:     branchName,
		AlertName:      alertName,
		Diagnosis:      diagnosis,
		Telemetry:      telemetry,
		ReviewFeedback: reviewFeedback,
	})"""

content = content.replace(old_sig, new_sig)
content = content.replace(old_data, new_data)

with open('internal/minion/fixer.go', 'w') as f:
    f.write(content)
