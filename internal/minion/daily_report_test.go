package minion

import (
	"strings"
	"testing"
)

func TestParseDailyHealthStatus(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectHealthy bool
		expectStatus  string
	}{
		{
			name:          "explicit HEALTHY",
			input:         "All nodes ready.\n\nHEALTH_STATUS: HEALTHY",
			expectHealthy: true,
			expectStatus:  "HEALTHY",
		},
		{
			name:          "markdown decorated HEALTHY",
			input:         "Fleet normal.\n**`HEALTH_STATUS: HEALTHY`**",
			expectHealthy: true,
			expectStatus:  "HEALTHY",
		},
		{
			name:          "explicit ATTENTION_NEEDED",
			input:         "1 pod restarting.\n\nHEALTH_STATUS: ATTENTION_NEEDED",
			expectHealthy: false,
			expectStatus:  "ATTENTION_NEEDED",
		},
		{
			name:          "empty or ambiguous defaults to ATTENTION_NEEDED",
			input:         "Partial report without conclusion.",
			expectHealthy: false,
			expectStatus:  "ATTENTION_NEEDED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			healthy, status := ParseDailyHealthStatus(tc.input)
			if healthy != tc.expectHealthy || status != tc.expectStatus {
				t.Errorf("ParseDailyHealthStatus() = (%v, %q), want (%v, %q)", healthy, status, tc.expectHealthy, tc.expectStatus)
			}
		})
	}
}

func TestParseDailyProactiveActions(t *testing.T) {
	raw := strings.Join([]string{
		"## Daily Fleet Check — 2026-11-23",
		"**Executive Status**: Attention needed on two root causes.",
		"",
		"### Actionable Recommendations",
		"1. Bump Grafana memory limit.",
		"2. Restart stalled review-server rollout.",
		"",
		"PROACTIVE_ACTION: FIX_VIA_PR | observability/grafana | Grafana OOMKilled at 128Mi; raise memory limit in values.yaml.",
		"- `PROACTIVE_ACTION: FIX_RUNTIME | ROLLOUT_RESTART observability/deployment/grafana | Clear SQLite lock cleanly.`",
		"PROACTIVE_ACTION: FIX_RUNTIME | ROLLOUT_RESTART data/deployment/postgres-primary | Should be rejected by allowlist.",
		"PROACTIVE_ACTION: FIX_VIA_PR | observability/grafana | Duplicate target should be deduplicated.",
		"PROACTIVE_ACTION: INVALID_MODE | apps/foo | Missing valid mode.",
		"",
		"HEALTH_STATUS: ATTENTION_NEEDED",
	}, "\n")

	cleanReport, actions := ParseDailyProactiveActions(raw)
	if len(actions) != 2 {
		t.Fatalf("expected 2 valid deduplicated proactive actions, got %d (%+v)", len(actions), actions)
	}

	if actions[0].Mode != ProactiveFixViaPR || actions[0].Namespace != "observability" || actions[0].Target != "grafana" {
		t.Errorf("unexpected first action: %+v", actions[0])
	}
	if !strings.Contains(actions[0].Diagnosis, "OOMKilled") {
		t.Errorf("expected diagnosis preserved on first action, got %q", actions[0].Diagnosis)
	}

	if actions[1].Mode != ProactiveFixRuntime || actions[1].RuntimeAction == nil || actions[1].RuntimeAction.TargetString() != "observability/deployment/grafana" {
		t.Errorf("unexpected second action: %+v", actions[1])
	}

	if strings.Contains(cleanReport, "PROACTIVE_ACTION:") {
		t.Errorf("expected PROACTIVE_ACTION lines stripped from cleanReport:\n%s", cleanReport)
	}
	healthy, status := ParseDailyHealthStatus(cleanReport)
	if healthy || status != "ATTENTION_NEEDED" {
		t.Errorf("ParseDailyHealthStatus(cleanReport) = (%v, %q), want (false, ATTENTION_NEEDED)", healthy, status)
	}
}
