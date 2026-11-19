package prompt

import (
	"strings"
	"testing"
)

func TestRenderTriagePrompt_Layer1EdgeKnowledge(t *testing.T) {
	data := TriageData{
		AlertName: "EndpointDown",
		Labels: map[string]string{
			"alertname": "EndpointDown",
			"http_url":  "http://10.10.0.4:8088/anon/",
			"node":      "gce10",
		},
		Annotations: map[string]string{
			"summary": "GCE10 File Server probe down",
		},
		Telemetry: "=== Active HTTP/TLS Probe ===\nProbe Status: FAILED (Network/Socket Error)\nError: dial tcp 10.10.0.4:8088: connect: connection refused",
	}

	rendered, err := RenderTriagePrompt(data)
	if err != nil {
		t.Fatalf("failed to render triage prompt: %v", err)
	}

	if !strings.Contains(rendered, "gce10 (:8088/anon/)") {
		t.Errorf("expected triage prompt to mention gce10 8088 upload server, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Connection Refused") {
		t.Errorf("expected triage prompt to mention Connection Refused as real incident signal, got:\n%s", rendered)
	}
}

func TestRenderFixerPrompt_Layer1EdgeKnowledge(t *testing.T) {
	data := FixerData{
		IssueNumber: 42,
		BranchName:  "fix/alert-42",
		AlertName:   "EndpointDown",
		Diagnosis:   "File server port dropped due to Caddy reload collision",
		Telemetry:   "Connection refused on 8088",
	}

	rendered, err := RenderFixerPrompt(data)
	if err != nil {
		t.Fatalf("failed to render fixer prompt: %v", err)
	}

	if !strings.Contains(rendered, "caddy/Caddyfile") {
		t.Errorf("expected fixer prompt to reference caddy/Caddyfile, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "scripts/gitops-sync.sh") {
		t.Errorf("expected fixer prompt to reference scripts/gitops-sync.sh, got:\n%s", rendered)
	}
}

func TestRenderPrompts_AccurateClusterTopology(t *testing.T) {
	triageRendered, err := RenderTriagePrompt(TriageData{AlertName: "PodOOMKilled"})
	if err != nil {
		t.Fatalf("failed to render triage prompt: %v", err)
	}
	fixerRendered, err := RenderFixerPrompt(FixerData{IssueNumber: 1, BranchName: "fix/1", AlertName: "PodOOMKilled"})
	if err != nil {
		t.Fatalf("failed to render fixer prompt: %v", err)
	}
	reviewerRendered, err := RenderReviewerPrompt(ReviewerData{PRNumber: 1, BranchName: "fix/1"})
	if err != nil {
		t.Fatalf("failed to render reviewer prompt: %v", err)
	}

	for name, body := range map[string]string{
		"triage":   triageRendered,
		"fixer":    fixerRendered,
		"reviewer": reviewerRendered,
	} {
		if !strings.Contains(body, "24GB RAM") {
			t.Errorf("%s prompt must report arm10 as 24GB RAM, got:\n%s", name, body)
		}
		if strings.Contains(body, "10GB RAM") {
			t.Errorf("%s prompt still contains stale 10GB RAM, got:\n%s", name, body)
		}
	}

	if !strings.Contains(triageRendered, "amd11 (NodePort 30006, port 8087)") {
		t.Errorf("triage prompt must document VietCalendar on amd11 (NodePort 30006, port 8087), got:\n%s", triageRendered)
	}
	if strings.Contains(triageRendered, "arm10 (NodePort 30006, port 8087)") || strings.Contains(triageRendered, "amd10 (:8080)") {
		t.Errorf("triage prompt still lists stale host for VietCalendar")
	}
	if !strings.Contains(fixerRendered, "30001-30016") {
		t.Errorf("fixer prompt must document NodePort range 30001-30016, got:\n%s", fixerRendered)
	}
	for _, want := range []string{
		"Service & Workload Check",
		"Helm Chart & Drift Check",
		"VERDICT: IGNORE",
		"VERDICT: ESCALATE_HUMAN",
		"VERDICT: FIX_VIA_PR",
	} {
		if !strings.Contains(triageRendered, want) {
			t.Errorf("triage prompt must require %q, got:\n%s", want, triageRendered)
		}
	}
}

func TestRenderDailyReportPrompt_ReadOnlyConstraints(t *testing.T) {
	rendered, err := RenderDailyReportPrompt(DailyReportData{
		Date:            "2026-11-20",
		Snapshot:        "=== Nodes ===\narm10 Ready=True",
		IncidentSummary: "0 firing incidents in last 24h",
	})
	if err != nil {
		t.Fatalf("failed to render daily report prompt: %v", err)
	}

	for _, want := range []string{
		"2026-11-20",
		"STRICTLY READ-ONLY",
		"arm10 Ready=True",
		"0 firing incidents",
		"HEALTH_STATUS: HEALTHY",
		"HEALTH_STATUS: ATTENTION_NEEDED",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("daily report prompt missing %q, got:\n%s", want, rendered)
		}
	}
}
