package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestWebhookHandler(t *testing.T) {
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
			name:           "Valid Resolved Alert",
			method:         "POST",
			payload:        `{"status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"HighCPUUsage"},"annotations":{"description":"CPU returned to normal"}}]}`,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Invalid JSON Payload",
			method:         "POST",
			payload:        `{"status":"firing", "alerts": [ bad json }`,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Multiple Alerts in Payload",
			method:         "POST",
			payload:        `{"status":"firing","alerts":[{"status":"firing","labels":{"alertname":"MemoryLeak"}},{"status":"firing","labels":{"alertname":"DiskSpaceLow"}}]}`,
			expectedStatus: http.StatusOK,
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
