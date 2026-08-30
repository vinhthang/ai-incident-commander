package minion

import (
	"os"
	"strings"
	"testing"
)

// A simple test to ensure the prompt contains the IGNORED instruction
func TestRunTriagePrompt(t *testing.T) {
	// Ensure that if we were to run it, the codebase contains the IGNORED logic
	content, err := os.ReadFile("triage.go")
	if err != nil {
		t.Fatalf("Failed to read triage.go: %v", err)
	}
	
	if !strings.Contains(string(content), "IGNORED") {
		t.Errorf("Triage minion does not contain IGNORED keyword logic")
	}
}
