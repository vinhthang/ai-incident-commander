package minion

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

func RunFixer(ctx context.Context, issueNumber int, branchName, alertName, diagnosis, telemetry, reviewFeedback string) string {
	ctx, span := tracer.Start(ctx, "RunFixer")
	defer span.End()

	span.SetAttributes(
		attribute.Int("github.issue", issueNumber),
		attribute.String("git.branch", branchName),
	)

	fixerCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	p, err := prompt.RenderFixerPrompt(prompt.FixerData{
		IssueNumber:    issueNumber,
		BranchName:     branchName,
		AlertName:      alertName,
		Diagnosis:      diagnosis,
		Telemetry:      telemetry,
		ReviewFeedback: reviewFeedback,
	})
	if err != nil {
		span.RecordError(err)
		log.Printf("Failed to render fixer prompt: %v", err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v", err)
	}

	span.AddEvent("Enqueueing agy CLI job for Fixer")
	log.Println("Submitting Fixer job to AGY queue...")

	result := EnqueueAgyJob(fixerCtx, p)

	if result.Err != nil {
		span.RecordError(result.Err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", result.Stderr))
		log.Printf("Fixer Minion failed: %v", result.Err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", result.Err, result.Stdout, result.Stderr)
	}

	span.AddEvent("Fixer Minion Executed Successfully")
	return fmt.Sprintf("✅ Fixer Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```", result.Stdout)
}
