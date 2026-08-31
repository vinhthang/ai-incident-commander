package minion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

func RunSeniorFixer(ctx context.Context, issueNumber int, branchName, originalDiagnosis, localDiff string) (bool, string) {
	ctx, span := tracer.Start(ctx, "RunSeniorFixer")
	defer span.End()

	span.SetAttributes(
		attribute.Int("github.issue", issueNumber),
		attribute.String("git.branch", branchName),
	)

	reviewerCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	p, err := prompt.RenderSeniorFixerPrompt(prompt.SeniorFixerData{
		IssueNumber: issueNumber,
		BranchName:  branchName,
		Diagnosis:   originalDiagnosis,
		LocalDiff:   localDiff,
	})
	if err != nil {
		span.RecordError(err)
		log.Printf("Failed to render senior fixer prompt: %v", err)
		return false, fmt.Sprintf("❌ Senior Fixer Minion failed to execute: %v", err)
	}

	span.AddEvent("Executing agy CLI for Senior Fixer")
	log.Println("Executing agy CLI for Senior Fixer...")

	cmd := exec.CommandContext(reviewerCtx, "/usr/local/bin/agy", "-p", p, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)

	err = cmd.Run()
	outputStr := outBuf.String()

	if err != nil {
		span.RecordError(err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", errBuf.String()))
		log.Printf("Senior Fixer Minion failed: %v", err)
		return false, fmt.Sprintf("❌ Senior Fixer Minion failed to execute: %v\n\nLogs:\n%s", err, errBuf.String())
	}

	span.AddEvent("Parsing Senior Fixer Minion Output")
	isApproved, verdict := ParseReviewDecision(outputStr)
	span.SetAttributes(
		attribute.Bool("senior_fixer.approved", isApproved),
		attribute.String("senior_fixer.verdict", verdict),
	)

	return isApproved, outputStr
}
