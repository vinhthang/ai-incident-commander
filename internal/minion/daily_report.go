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
