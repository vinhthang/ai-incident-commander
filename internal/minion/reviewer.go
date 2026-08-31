package minion

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

func ParseReviewDecision(output string) (bool, string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		cleaned := strings.Trim(line, "`*#_ ")
		if strings.EqualFold(cleaned, "APPROVED") {
			return true, "APPROVED"
		}
		if strings.EqualFold(cleaned, "REJECTED") {
			return false, "REJECTED"
		}
		break
	}
	return false, "UNKNOWN"
}

func RunReviewer(ctx context.Context, prNumber int, branchName, prDiff, originalDiagnosis string) (bool, string) {
	ctx, span := tracer.Start(ctx, "RunReviewer")
	defer span.End()

	span.SetAttributes(
		attribute.Int("github.pr", prNumber),
		attribute.String("git.branch", branchName),
	)

	reviewerCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	p, err := prompt.RenderReviewerPrompt(prompt.ReviewerData{
		PRNumber:          prNumber,
		BranchName:        branchName,
		OriginalDiagnosis: originalDiagnosis,
		PRDiff:            prDiff,
	})
	if err != nil {
		span.RecordError(err)
		log.Printf("Failed to render reviewer prompt: %v", err)
		return false, fmt.Sprintf("❌ Reviewer Minion failed to execute: %v", err)
	}

	span.AddEvent("Enqueueing agy CLI job for Reviewer")
	log.Println("Submitting Reviewer job to AGY queue...")

	result := EnqueueAgyJob(reviewerCtx, p)
	outputStr := result.Stdout

	if result.Err != nil {
		span.RecordError(result.Err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", result.Stderr))
		log.Printf("Reviewer Minion failed: %v", result.Err)
		return false, fmt.Sprintf("❌ Reviewer Minion failed to execute: %v\n\nLogs:\n%s", result.Err, result.Stderr)
	}

	span.AddEvent("Parsing Reviewer Minion Output")
	isApproved, verdict := ParseReviewDecision(outputStr)
	span.SetAttributes(
		attribute.Bool("reviewer.approved", isApproved),
		attribute.String("reviewer.verdict", verdict),
	)

	return isApproved, outputStr
}
