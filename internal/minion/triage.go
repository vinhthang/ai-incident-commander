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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	
	"vinhthang.dev/ai-incident-commander/internal/config"
)

var tracer = otel.Tracer("ai-incident-commander/minion")

func RunTriage(ctx context.Context, alertName string, labels, annotations map[string]string, telemetry string) string {
	ctx, span := tracer.Start(ctx, "RunTriage")
	defer span.End()
	
	span.SetAttributes(attribute.String("alert.name", alertName))

	prompt := fmt.Sprintf(`You are the Triage Minion for a Kubernetes GitOps cluster.

Your task is to analyze this Grafana Alert and the provided telemetry to formulate a root cause hypothesis.
Use your tools to query the codebase, read configurations, or search logs to validate the alert.
If the alert is invalid, unknown, a false positive, or there is not enough information to proceed with a fix, output EXACTLY the word 'IGNORED' on the last line of your response.
Otherwise, provide a clear diagnosis and suggested fix action for the Fixer Minion.

Alert: %s
Labels: %v
Annotations: %v
Telemetry:
%s`, alertName, labels, annotations, telemetry)

	span.AddEvent("Executing agy CLI for Triage")
	log.Println("Executing agy CLI for Triage...")
	
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
		log.Printf("Triage Minion failed: %v", err)
		return "⚠️ Triage Minion encountered an error."
	}
	
	span.AddEvent("Parsing Triage Minion Output")
	if strings.Contains(outputStr, "IGNORED") {
		span.SetAttributes(attribute.Bool("triage.ignored", true))
		return "IGNORED"
	}

	span.SetAttributes(attribute.Bool("triage.ignored", false))
	return outputStr
}
