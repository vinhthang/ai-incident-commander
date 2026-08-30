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
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

var tracer = otel.Tracer("ai-incident-commander/minion")

// IsIgnored parses the output to determine if the Triage Minion decided to ignore the alert.
func IsIgnored(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		cleaned := strings.Trim(line, "`*#_ ")
		if strings.EqualFold(cleaned, "IGNORED") {
			return true
		}
		break
	}
	return false
}

func RunTriage(ctx context.Context, alertName string, labels, annotations map[string]string, telemetry string) string {
	ctx, span := tracer.Start(ctx, "RunTriage")
	defer span.End()

	span.SetAttributes(attribute.String("alert.name", alertName))

	triageCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	p, err := prompt.RenderTriagePrompt(prompt.TriageData{
		AlertName:   alertName,
		Labels:      labels,
		Annotations: annotations,
		Telemetry:   telemetry,
	})
	if err != nil {
		span.RecordError(err)
		log.Printf("Failed to render triage prompt: %v", err)
		return "⚠️ Triage Minion encountered an error."
	}

	span.AddEvent("Executing agy CLI for Triage")
	log.Println("Executing agy CLI for Triage...")

	cmd := exec.CommandContext(triageCtx, "/usr/local/bin/agy", "-p", p, "--dangerously-skip-permissions")
	cmd.Dir = config.WorkspaceDir

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &outBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)

	err = cmd.Run()
	outputStr := outBuf.String()

	if err != nil {
		span.RecordError(err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", errBuf.String()))
		log.Printf("Triage Minion failed: %v", err)
		return "⚠️ Triage Minion encountered an error."
	}

	span.AddEvent("Parsing Triage Minion Output")
	if IsIgnored(outputStr) {
		span.SetAttributes(attribute.Bool("triage.ignored", true))
		return "IGNORED"
	}

	span.SetAttributes(attribute.Bool("triage.ignored", false))
	return outputStr
}
