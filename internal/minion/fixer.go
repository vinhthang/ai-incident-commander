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

func RunFixer(ctx context.Context, issueNumber int, branchName, alertName, diagnosis, telemetry string) string {
	ctx, span := tracer.Start(ctx, "RunFixer")
	defer span.End()

	span.SetAttributes(
		attribute.Int("github.issue", issueNumber),
		attribute.String("git.branch", branchName),
	)

	fixerCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	p, err := prompt.RenderFixerPrompt(prompt.FixerData{
		IssueNumber: issueNumber,
		BranchName:  branchName,
		AlertName:   alertName,
		Diagnosis:   diagnosis,
		Telemetry:   telemetry,
	})
	if err != nil {
		span.RecordError(err)
		log.Printf("Failed to render fixer prompt: %v", err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v", err)
	}

	span.AddEvent("Executing agy CLI for Fixer")
	log.Println("Executing agy CLI for Fixer...")

	cmd := exec.CommandContext(fixerCtx, "/usr/local/bin/agy", "-p", p, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)

	err = cmd.Run()

	if err != nil {
		span.RecordError(err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", errBuf.String()))
		log.Printf("Fixer Minion failed: %v", err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", err, outBuf.String(), errBuf.String())
	}

	span.AddEvent("Fixer Minion Executed Successfully")
	return fmt.Sprintf("✅ Fixer Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```", outBuf.String())
}
