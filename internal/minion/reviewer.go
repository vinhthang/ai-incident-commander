package minion

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func RunReviewer(prNumber int, branchName, prDiff, originalDiagnosis string) (bool, string) {
	prompt := fmt.Sprintf(`You are the Reviewer Minion. Pull Request #%d (branch '%s') has been opened to address this Triage Diagnosis:
%s

Here is the exact git diff of the PR:
%s

Use your tools to explore the codebase on branch '%s' to audit the blast radius of these changes.
Analyze if it safely and correctly addresses the root cause without violating architectural governance or introducing regressions.
If the changes are perfect, output EXACTLY the word 'APPROVED' on the last line.
Otherwise, output 'REJECTED' on the last line and explain the problem above it.`, prNumber, branchName, originalDiagnosis, prDiff, branchName)
	
	cmd := exec.Command("/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir
	
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	outputStr := outBuf.String()
	
	if err != nil {
		log.Printf("Reviewer Minion failed: %v", err)
		return false, fmt.Sprintf("❌ Reviewer Minion failed to execute: %v\n\nLogs:\n%s", err, errBuf.String())
	}

	isApproved := strings.Contains(outputStr, "APPROVED")
	return isApproved, outputStr
}
