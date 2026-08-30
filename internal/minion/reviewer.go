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

func RunReviewer(prNumber int, prDiff, originalDiagnosis string) (bool, string) {
	prompt := fmt.Sprintf("You are the Reviewer Minion. Pull Request #%d has been opened to address this Triage Diagnosis:\n%s\n\nHere is the exact git diff of the PR:\n```diff\n%s\n```\n\nAnalyze the diff. Does it safely and correctly address the root cause? Are there any syntax errors, port conflicts, or regressions? If the changes are perfect, output EXACTLY the word 'APPROVED' on the last line. Otherwise, output 'REJECTED' on the last line and explain the problem above it.", prNumber, originalDiagnosis, prDiff)
	
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
