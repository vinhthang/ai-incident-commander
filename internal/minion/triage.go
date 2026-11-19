package minion

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

var tracer = otel.Tracer("ai-incident-commander/minion")

// TriageAction represents the explicit post-diagnostic conclusion returned by Triage Minion.
type TriageAction string

const (
	ActionIgnore        TriageAction = "IGNORE"
	ActionEscalateHuman TriageAction = "ESCALATE_HUMAN"
	ActionFixViaPR      TriageAction = "FIX_VIA_PR"
)

// TriageResult captures the structured triage conclusion, confidence, and raw report.
type TriageResult struct {
	Action     TriageAction
	Confidence string
	Summary    string
	Err        error
}

// ParseTriageDecision extracts the final verdict and Confidence level from triage output.
// If FIX_VIA_PR is requested without HIGH confidence, it safely downgrades to ESCALATE_HUMAN.
// Any ambiguous or unrecognized output defaults safely to ESCALATE_HUMAN.
func ParseTriageDecision(output string) (TriageAction, string) {
	confidence := extractConfidence(output)

	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		cleaned := strings.ToUpper(strings.TrimSpace(strings.Trim(line, "`*#_ ")))
		switch cleaned {
		case "VERDICT: IGNORE", "IGNORED":
			return ActionIgnore, confidence
		case "VERDICT: ESCALATE_HUMAN":
			return ActionEscalateHuman, confidence
		case "VERDICT: FIX_VIA_PR":
			if confidence == "HIGH" {
				return ActionFixViaPR, confidence
			}
			return ActionEscalateHuman, confidence
		default:
			return ActionEscalateHuman, confidence
		}
	}
	return ActionEscalateHuman, confidence
}

func extractConfidence(output string) string {
	for _, line := range strings.Split(output, "\n") {
		norm := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(line), "*", ""))
		if !strings.HasPrefix(norm, "CONFIDENCE:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(norm, "CONFIDENCE:"))
		for _, level := range []string{"HIGH", "MEDIUM", "LOW"} {
			if strings.HasPrefix(val, level) {
				return level
			}
		}
	}
	return "UNKNOWN"
}

// IsIgnored parses the output to determine if the Triage Minion decided to ignore the alert.
func IsIgnored(output string) bool {
	action, _ := ParseTriageDecision(output)
	return action == ActionIgnore
}

// RunDiagnosticTriage runs the diagnostic-first triage minion and parses the 3-way verdict.
func RunDiagnosticTriage(ctx context.Context, alertName string, labels, annotations map[string]string, telemetry string) TriageResult {
	ctx, span := tracer.Start(ctx, "RunDiagnosticTriage")
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
		return TriageResult{
			Action:     ActionEscalateHuman,
			Confidence: "UNKNOWN",
			Summary:    fmt.Sprintf("⚠️ Triage prompt render error: %v", err),
			Err:        err,
		}
	}

	span.AddEvent("Enqueueing agy CLI job for Diagnostic Triage")
	log.Println("Submitting Diagnostic Triage job to AGY queue...")

	result := EnqueueAgyJob(triageCtx, p)
	if result.Err != nil {
		span.RecordError(result.Err)
		span.AddEvent(fmt.Sprintf("agy CLI error: %v", result.Stderr))
		log.Printf("Diagnostic Triage Minion failed: %v", result.Err)
		return TriageResult{
			Action:     ActionEscalateHuman,
			Confidence: "UNKNOWN",
			Summary:    fmt.Sprintf("⚠️ Triage Minion encountered an error: %v", result.Err),
			Err:        result.Err,
		}
	}

	action, confidence := ParseTriageDecision(result.Stdout)
	span.SetAttributes(
		attribute.String("triage.verdict", string(action)),
		attribute.String("triage.confidence", confidence),
	)

	return TriageResult{
		Action:     action,
		Confidence: confidence,
		Summary:    result.Stdout,
	}
}

// RunTriage remains as a backwards-compatible wrapper returning "IGNORED" or report text.
func RunTriage(ctx context.Context, alertName string, labels, annotations map[string]string, telemetry string) string {
	res := RunDiagnosticTriage(ctx, alertName, labels, annotations, telemetry)
	if res.Err != nil {
		return "⚠️ Triage Minion encountered an error."
	}
	if res.Action == ActionIgnore {
		return "IGNORED"
	}
	return res.Summary
}
