package minion

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"

	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/config"
)

func RunFixer(ctx context.Context, issueNumber int, branchName, alertName, diagnosis, telemetry string) string {
	ctx, span := tracer.Start(ctx, "RunFixer")
	defer span.End()
	
	span.SetAttributes(
		attribute.Int("github.issue", issueNumber),
		attribute.String("git.branch", branchName),
	)

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
	
	span.AddEvent("Executing agy CLI for Fixer")
	log.Println("Executing agy CLI for Fixer...")

	cmd := exec.CommandContext(ctx, "/usr/local/bin/agy", "-p", prompt, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir
	
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	
	err := cmd.Run()
	
	if err != nil {
		span.RecordError(err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", errBuf.String()))
		log.Printf("Fixer Minion failed: %v", err)
		return fmt.Sprintf("❌ Fixer Minion encountered an error: %v\n\n### Stdout\n```\n%s\n```\n### Stderr\n```\n%s\n```", err, outBuf.String(), errBuf.String())
	}
	
	span.AddEvent("Fixer Minion Executed Successfully")
	return fmt.Sprintf("✅ Fixer Minion executed successfully.\n\n### Execution Logs\n```\n%s\n```", outBuf.String())
}
