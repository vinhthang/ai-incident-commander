package main

import (
	"log"
	"net/http"
	"os"

	"vinhthang.dev/ai-incident-commander/internal/config"
	"vinhthang.dev/ai-incident-commander/internal/github"
	"vinhthang.dev/ai-incident-commander/internal/webhook"
	"vinhthang.dev/ai-incident-commander/internal/workspace"
)

func main() {
	if config.GithubToken == "" || config.GeminiAPIKey == "" {
		log.Println("WARNING: GITHUB_TOKEN or GEMINI_API_KEY is not set. API calls will fail.")
	}

	github.InitClient()
	workspace.InitWorkspace()

	http.HandleFunc("/webhook", webhook.HandleWebhook)
	
	port := os.Getenv("PORT")
	if port == "" {
		port = "8085"
	}
	
	log.Printf("🚀 Modular AI Incident Commander starting on :%s (model: %s)", port, config.GeminiModel)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
