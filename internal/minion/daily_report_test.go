package minion

import "testing"

func TestParseDailyHealthStatus(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectHealthy  bool
		expectStatus   string
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
