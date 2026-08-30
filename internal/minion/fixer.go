package minion

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func RunFixer(issueNumber int, branchName, alertName, diagnosis, telemetry string) string {
	prompt := fmt.Sprintf(`You are the Fixer Minion. GitHub Issue #%d reports an alert '%s'.

Triage Diagnosis:
%s

Telemetry:
%s

You are running in the cloned repository on branch '%s'.
Analyze the repository locally, determine the root cause, and apply the required configuration or code fix.
Commit the changes and push them to the origin branch '%s'.
Do NOT push to main. A Pull Request will be opened automatically for your branch.
Log a summary of your actions so they can be posted as a comment on the PR.`, issueNumber, alertName, diagnosis, telemetry, branchName, branchName)
	
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir
	
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	
	if err != nil {
		log.Printf("Fixer Minion failed: %v", err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", err, outBuf.String(), errBuf.String())
	}
	return fmt.Sprintf("✅ Fixer Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```", outBuf.String())
}
