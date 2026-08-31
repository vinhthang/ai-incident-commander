package minion

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel/attribute"

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

	span.AddEvent("Enqueueing agy CLI job for Senior Fixer")
	log.Println("Submitting Senior Fixer job to AGY queue...")

	result := EnqueueAgyJob(reviewerCtx, p)
	outputStr := result.Stdout

	if result.Err != nil {
		span.RecordError(result.Err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", result.Stderr))
		log.Printf("Senior Fixer Minion failed: %v", result.Err)
		return false, fmt.Sprintf("❌ Senior Fixer Minion failed to execute: %v\n\nLogs:\n%s", result.Err, result.Stderr)
	}

	span.AddEvent("Parsing Senior Fixer Minion Output")
	isApproved, verdict := ParseReviewDecision(outputStr)
	span.SetAttributes(
		attribute.Bool("senior_fixer.approved", isApproved),
		attribute.String("senior_fixer.verdict", verdict),
	)

	return isApproved, outputStr
}
