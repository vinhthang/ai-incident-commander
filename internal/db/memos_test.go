package db

import (
	"context"
	"strings"
	"testing"
)

func TestFormatDailyMemosNote(t *testing.T) {
	note := FormatDailyMemosNote("2026-11-20", "HEALTHY", "All systems nominal.")
	for _, want := range []string{
		"2026-11-20",
		"HEALTHY",
		"All systems nominal.",
		"#DailyFleetCheck",
		"#AgyReport",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("formatted note missing %q, got:\n%s", want, note)
		}
	}

	if err := InsertMemosReport(context.Background(), "", note, false); err != nil {
		t.Fatalf("expected empty Memos DSN to no-op cleanly, got err=%v", err)
	}
}

func TestFormatAppIncidentMemosNote(t *testing.T) {
	note := FormatAppIncidentMemosNote("orders", "/api/v1/pay", "500", "4bf92f35", "Null pointer in handler.")
	for _, want := range []string{
		"orders",
		"/api/v1/pay",
		"500",
		"4bf92f35",
		"Null pointer in handler.",
		"#AppIncident",
		"#AgyTriage",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("app incident note missing %q, got:\n%s", want, note)
		}
	}

	noTrace := FormatAppIncidentMemosNote("orders", "/api/v1/pay", "500", "", "Timeout")
	if !strings.Contains(noTrace, "N/A") {
		t.Errorf("expected empty traceID to render N/A, got:\n%s", noTrace)
	}
}

func TestFormatRemediationMemosNote(t *testing.T) {
	note := FormatRemediationMemosNote(
		"daily-report",
		"runtime",
		"observability/deployment/grafana",
		"runtime_verified",
		"SQLite database lock cleared by rollout restart.",
		"deployment.apps/grafana rolled out cleanly.",
		"#346",
		"",
	)
	for _, want := range []string{
		"daily-report",
		"runtime",
		"observability/deployment/grafana",
		"runtime_verified",
		"SQLite database lock cleared by rollout restart.",
		"deployment.apps/grafana rolled out cleanly.",
		"#346",
		"N/A",
		"#AutoRemediation",
		"#AgyFix",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("remediation note missing %q, got:\n%s", want, note)
		}
	}
}
