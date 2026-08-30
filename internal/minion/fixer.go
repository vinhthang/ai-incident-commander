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
	prompt := fmt.Sprintf("You are the Fixer Minion. GitHub Issue #%d reports an alert '%s'.\n\nTriage Diagnosis:\n%s\n\nTelemetry:\n%s\n\nAnalyze the repository locally on the current branch, determine the root cause, apply the required configuration or code fix, commit the changes, and push to the origin branch '%s' directly. Do NOT open a PR, just push.", issueNumber, alertName, diagnosis, telemetry, branchName)
	
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
