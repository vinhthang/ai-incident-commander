package minion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"golang.org/x/time/rate"
	"vinhthang.dev/ai-incident-commander/internal/config"
)

var geminiLimiter = rate.NewLimiter(rate.Every(21*time.Second), 1)

func RunTriage(alertName string, labels, annotations map[string]string, telemetry string) string {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", config.GeminiModel, config.GeminiAPIKey)
	prompt := fmt.Sprintf("You are the Triage Minion for a Kubernetes GitOps cluster. Analyze this Grafana Alert and the provided telemetry to formulate a brief root cause hypothesis and suggested fix action.\n\nAlert: %s\nLabels: %v\nAnnotations: %v\nTelemetry:\n%s", alertName, labels, annotations, telemetry)

	reqBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]interface{}{{"text": prompt}}},
		},
	})

	geminiLimiter.Wait(context.Background())
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		log.Printf("Gemini triage request error: %v", err)
		return "⚠️ Failed to contact Gemini API for triage."
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "⚠️ Failed to contact Gemini API for triage (Non-200)."
	}

	var res map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&res)

	if candidates, ok := res["candidates"].([]interface{}); ok && len(candidates) > 0 {
		if content, ok := candidates[0].(map[string]interface{})["content"].(map[string]interface{}); ok {
			if parts, ok := content["parts"].([]interface{}); ok && len(parts) > 0 {
				if text, ok := parts[0].(map[string]interface{})["text"].(string); ok {
					return text
				}
			}
		}
	}
	return "Analyzed alert, proceeding with Fixer."
}
