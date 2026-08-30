package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Reset global state between tests
func resetState() {
	stateMu.Lock()
	defer stateMu.Unlock()
	activeIncidents = make(map[string]*IncidentState)
	cooldownMins = 5 * time.Minute // Default value
}

func TestIncidentLockAcquisition(t *testing.T) {
	resetState()
	
	alertName := "TestAlert"

	// 1. Initial firing should acquire lock
	if !acquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected to acquire lock on new firing alert")
	}

	// 2. Immediate subsequent firing should be dropped (debounced)
	if acquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected lock acquisition to fail for concurrent webhook stampede")
	}

	// 3. Status is now TRIAGING
	stateMu.Lock()
	state := activeIncidents[alertName]
	if state.Status != "TRIAGING" {
		t.Errorf("Expected status TRIAGING, got %s", state.Status)
	}
	stateMu.Unlock()

	// 4. Release lock to COOLING_DOWN
	releaseIncidentLock(alertName, "COOLING_DOWN")

	// 5. Try firing again immediately, should be dropped by cooldown
	if acquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected lock acquisition to fail due to 5-minute cooldown")
	}

	// 6. Artificially expire the cooldown
	stateMu.Lock()
	activeIncidents[alertName].LastAttempt = time.Now().Add(-10 * time.Minute)
	stateMu.Unlock()

	// 7. Should now acquire lock again for a new attempt
	if !acquireIncidentLock(alertName, "firing") {
		t.Errorf("Expected to acquire lock after cooldown expired")
	}
}

func TestResolveNonExistentIncident(t *testing.T) {
	resetState()
	
	// A resolved webhook comes in for an alert we aren't tracking
	if acquireIncidentLock("GhostAlert", "resolved") {
		t.Errorf("Should not acquire lock or track resolved incidents that have no active state")
	}
}

func TestFetchTelemetryMissingLabels(t *testing.T) {
	labels := map[string]string{
		"alertname": "BadAlert",
	}

	out := fetchTelemetry(labels)
	if !strings.Contains(out, "No namespace or pod specified") {
		t.Errorf("Expected missing labels message, got: %s", out)
	}
}

func TestWebhookHandler(t *testing.T) {
	resetState()
	
	tests := []struct {
		name           string
		method         string
		payload        string
		expectedStatus int
	}{
		{
			name:           "Valid New Alert",
			method:         "POST",
			payload:        `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"HighCPUUsage"},"annotations":{"description":"CPU is over 90%"}}]}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Multiple Alerts in Payload (Stampede Simulation)",
			method:         "POST",
			payload:        `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"HighCPUUsage"}},{"status":"firing","labels":{"alertname":"HighCPUUsage"}}]}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Invalid JSON Payload",
			method:         "POST",
			payload:        `{"status":"firing", "alerts": [ bad json }`,
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "/webhook", bytes.NewBuffer([]byte(tt.payload)))
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(handleWebhook)

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("Handler returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}
		})
	}

	// Give goroutines time to execute lock logic before verifying state map length
	time.Sleep(100 * time.Millisecond)
	
	stateMu.RLock()
	defer stateMu.RUnlock()
	// HighCPUUsage should only be tracked once, despite duplicate firing payload
	if state, ok := activeIncidents["HighCPUUsage"]; !ok {
		t.Errorf("HighCPUUsage alert was not recorded in state map")
	} else if state.Status != "TRIAGING" {
		t.Errorf("Expected status TRIAGING, got %s", state.Status)
	}
}

func TestConcurrentWebhookStampede(t *testing.T) {
	resetState()
	
	// Simulate 10 simultaneous webhooks for the same alert
	var wg sync.WaitGroup
	handler := http.HandlerFunc(handleWebhook)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"ConcurrentAlert"}}]}`
			req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer([]byte(payload)))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	stateMu.RLock()
	defer stateMu.RUnlock()

	// Only 1 instance should exist in the map
	if len(activeIncidents) != 1 {
		t.Errorf("Expected exactly 1 incident tracked, found %d", len(activeIncidents))
	}
}

func TestGeminiModelAvailability(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping TestGeminiModelAvailability: GEMINI_API_KEY environment variable not set")
	}

	model := getEnv("GEMINI_MODEL", "gemini-3.5-flash")
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)

	reqBody, err := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]interface{}{{"text": "Ping"}}},
		},
	})
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("Network request to Gemini API failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Gemini model '%s' returned HTTP %d: %s. Model may be deprecated, renamed, or not found.", model, resp.StatusCode, string(bodyBytes))
	}

	var res map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		t.Fatalf("Failed to unmarshal Gemini API JSON: %v", err)
	}

	candidates, ok := res["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		t.Fatalf("Gemini response did not contain candidates: %s", string(bodyBytes))
	}
}
