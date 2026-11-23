package minion

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"vinhthang.dev/ai-incident-commander/internal/k8s"
	"vinhthang.dev/ai-incident-commander/internal/prompt"
)

// ProactiveActionMode identifies the execution path for a proactive daily-audit finding.
type ProactiveActionMode string

const (
	ProactiveFixViaPR   ProactiveActionMode = "FIX_VIA_PR"
	ProactiveFixRuntime ProactiveActionMode = "FIX_RUNTIME"
)

// DailyProactiveAction represents a validated proactive fix candidate emitted by the daily fleet audit.
type DailyProactiveAction struct {
	Mode          ProactiveActionMode
	Namespace     string
	Target        string
	Diagnosis     string
	RuntimeAction *k8s.RuntimeAction
}

// ParseDailyProactiveActions extracts valid PROACTIVE_ACTION lines and returns a cleaned report.
func ParseDailyProactiveActions(output string) (string, []DailyProactiveAction) {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	actions := make([]DailyProactiveAction, 0)
	seen := make(map[string]struct{})

	for _, line := range lines {
		stripped := strings.NewReplacer("`", "", "*", "").Replace(strings.TrimSpace(line))
		stripped = strings.TrimSpace(strings.TrimLeft(stripped, "- "))
		if len(stripped) >= len("PROACTIVE_ACTION:") && strings.EqualFold(stripped[:len("PROACTIVE_ACTION:")], "PROACTIVE_ACTION:") {
			payload := strings.TrimSpace(stripped[len("PROACTIVE_ACTION:"):])
			if act, key, ok := parseProactiveActionPayload(payload); ok {
				if _, exists := seen[key]; !exists {
					seen[key] = struct{}{}
					actions = append(actions, act)
				}
			}
			continue
		}
		kept = append(kept, line)
	}

	return strings.TrimSpace(strings.Join(kept, "\n")), actions
}

func parseProactiveActionPayload(payload string) (DailyProactiveAction, string, bool) {
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) != 3 {
		return DailyProactiveAction{}, "", false
	}

	modeStr := strings.ToUpper(strings.TrimSpace(parts[0]))
	targetSpec := strings.TrimSpace(parts[1])
	diagnosis := strings.TrimSpace(parts[2])
	if diagnosis == "" {
		return DailyProactiveAction{}, "", false
	}

	switch ProactiveActionMode(modeStr) {
	case ProactiveFixViaPR:
		slashParts := strings.Split(targetSpec, "/")
		if len(slashParts) != 2 {
			return DailyProactiveAction{}, "", false
		}
		ns := strings.TrimSpace(slashParts[0])
		target := strings.TrimSpace(slashParts[1])
		if ns == "" || target == "" || strings.ContainsAny(ns+target, " \t") {
			return DailyProactiveAction{}, "", false
		}
		key := "pr:" + ns + "/" + target
		return DailyProactiveAction{
			Mode:      ProactiveFixViaPR,
			Namespace: ns,
			Target:    target,
			Diagnosis: diagnosis,
		}, key, true

	case ProactiveFixRuntime:
		rtAct, err := k8s.ParseRuntimeActionSpec(targetSpec)
		if err != nil {
			return DailyProactiveAction{}, "", false
		}
		rtAct.Reason = diagnosis
		key := "runtime:" + rtAct.TargetString()
		return DailyProactiveAction{
			Mode:          ProactiveFixRuntime,
			Namespace:     rtAct.Namespace,
			Target:        rtAct.Name,
			Diagnosis:     diagnosis,
			RuntimeAction: &rtAct,
		}, key, true

	default:
		return DailyProactiveAction{}, "", false
	}
}

// ParseDailyHealthStatus parses the final HEALTH_STATUS line from the daily audit output.
// Unrecognized or incomplete reports default conservatively to ATTENTION_NEEDED.
func ParseDailyHealthStatus(output string) (healthy bool, status string) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		cleaned := strings.ToUpper(strings.TrimSpace(strings.Trim(line, "`*#_ ")))
		if cleaned == "HEALTH_STATUS: HEALTHY" {
			return true, "HEALTHY"
		}
		if cleaned == "HEALTH_STATUS: ATTENTION_NEEDED" {
			return false, "ATTENTION_NEEDED"
		}
		break
	}
	return false, "ATTENTION_NEEDED"
}

// RunDailyReport submits the read-only daily fleet audit prompt through the serialized AGY queue.
func RunDailyReport(ctx context.Context, dateStr, snapshot, incidentSummary string) (report string, healthy bool, err error) {
	ctx, span := tracer.Start(ctx, "RunDailyReport")
	defer span.End()

	span.SetAttributes(attribute.String("report.date", dateStr))

	reportCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	p, err := prompt.RenderDailyReportPrompt(prompt.DailyReportData{
		Date:            dateStr,
		Snapshot:        snapshot,
		IncidentSummary: incidentSummary,
	})
	if err != nil {
		span.RecordError(err)
		return "", false, fmt.Errorf("render daily report prompt: %w", err)
	}

	log.Printf("Submitting Daily Fleet Check (%s) to AGY queue...", dateStr)
	res := EnqueueAgyJob(reportCtx, p)
	if res.Err != nil {
		span.RecordError(res.Err)
		return res.Stdout, false, fmt.Errorf("agy daily report execution failed: %w", res.Err)
	}

	healthy, status := ParseDailyHealthStatus(res.Stdout)
	span.SetAttributes(
		attribute.Bool("report.healthy", healthy),
		attribute.String("report.status", status),
	)
	return res.Stdout, healthy, nil
}
