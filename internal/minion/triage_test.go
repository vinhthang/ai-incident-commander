package minion

import (
	"testing"
)

func TestIsIgnored(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "clean ignored on last line",
			input:    "This alert appears to be a synthetic test.\n\nIGNORED",
			expected: true,
		},
		{
			name:     "ignored with markdown backticks",
			input:    "No action required for this alert.\n`IGNORED`",
			expected: true,
		},
		{
			name:     "ignored with bold markdown",
			input:    "No action required.\n**IGNORED**",
			expected: true,
		},
		{
			name:     "lowercase ignored",
			input:    "Analysis done.\nignored",
			expected: true,
		},
		{
			name:     "trailing whitespace after ignored",
			input:    "Diagnostic summary.\nIGNORED   \n\n  ",
			expected: true,
		},
		{
			name:     "CRITICAL: should NOT be ignored explanation",
			input:    "This alert is critical and should NOT be IGNORED.\nRoot cause: Database deadlock in user service.",
			expected: false,
		},
		{
			name:     "ignored mentioned in body but real diagnosis at end",
			input:    "Previously IGNORED alerts have been seen here, but this is a genuine outage.\nApply Helm memory limit fix.",
			expected: false,
		},
		{
			name:     "empty input",
			input:    "",
			expected: false,
		},
		{
			name:     "only whitespace",
			input:    "   \n\t\n  ",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsIgnored(tc.input)
			if result != tc.expected {
				t.Errorf("IsIgnored() = %v, expected %v for input:\n%s", result, tc.expected, tc.input)
			}
		})
	}
}

func TestParseReviewDecision(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		expectedApproved bool
		expectedVerdict  string
	}{
		{
			name:             "clean approved on last line",
			input:            "The diff correctly updates the Helm resource limits and has zero blast radius.\n\nAPPROVED",
			expectedApproved: true,
			expectedVerdict:  "APPROVED",
		},
		{
			name:             "approved with markdown formatting",
			input:            "All checks pass.\n**APPROVED**",
			expectedApproved: true,
			expectedVerdict:  "APPROVED",
		},
		{
			name:             "clean rejected on last line",
			input:            "The diff modifies the wrong service and introduces a security flaw.\n\nREJECTED",
			expectedApproved: false,
			expectedVerdict:  "REJECTED",
		},
		{
			name:             "CRITICAL: rejected mentioning NOT APPROVED in body",
			input:            "This PR is REJECTED because it was not APPROVED by team lead standards.\n\nREJECTED",
			expectedApproved: false,
			expectedVerdict:  "REJECTED",
		},
		{
			name:             "ambiguous response defaults to safe unapproved",
			input:            "I am unsure whether this change is safe. Needs manual review.",
			expectedApproved: false,
			expectedVerdict:  "UNKNOWN",
		},
		{
			name:             "empty input",
			input:            "",
			expectedApproved: false,
			expectedVerdict:  "UNKNOWN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			approved, verdict := ParseReviewDecision(tc.input)
			if approved != tc.expectedApproved || verdict != tc.expectedVerdict {
				t.Errorf("ParseReviewDecision() = (%v, %q), expected (%v, %q) for input:\n%s",
					approved, verdict, tc.expectedApproved, tc.expectedVerdict, tc.input)
			}
		})
	}
}
