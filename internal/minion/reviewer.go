package minion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func RunReviewer(ctx context.Context, prNumber int, branchName, prDiff, originalDiagnosis string) (bool, string) {
	ctx, span := tracer.Start(ctx, "RunReviewer")
	defer span.End()

	span.SetAttributes(
		attribute.Int("github.pr", prNumber),
		attribute.String("git.branch", branchName),
	)

	prompt := fmt.Sprintf(`You are the Reviewer Minion. Pull Request #%d (branch '%s') has been opened to address this Triage Diagnosis:
%s

Here is the exact git diff of the PR:
%s

Use your tools to explore the codebase on branch '%s' to audit the blast radius of these changes.
Analyze if it safely and correctly addresses the root cause without violating architectural governance or introducing regressions.
If the changes are perfect, output EXACTLY the word 'APPROVED' on the last line.
Otherwise, output 'REJECTED' on the last line and explain the problem above it.`, prNumber, branchName, originalDiagnosis, prDiff, branchName)
	
	span.AddEvent("Executing agy CLI for Reviewer")
	log.Println("Executing agy CLI for Reviewer...")

	cmd := exec.CommandContext(ctx, "/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir
	
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	outputStr := outBuf.String()
	
	if err != nil {
		span.RecordError(err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", errBuf.String()))
		log.Printf("Reviewer Minion failed: %v", err)
		return false, fmt.Sprintf("❌ Reviewer Minion failed to execute: %v\n\nLogs:\n%s", err, errBuf.String())
	}

	span.AddEvent("Parsing Reviewer Minion Output")
	isApproved := strings.Contains(outputStr, "APPROVED")
	span.SetAttributes(attribute.Bool("reviewer.approved", isApproved))

	return isApproved, outputStr
}
