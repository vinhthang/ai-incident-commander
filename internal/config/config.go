package config

import (
	"os"
	"time"
)

var (
	GithubToken  = os.Getenv("GITHUB_TOKEN")
	GeminiAPIKey = os.Getenv("GEMINI_API_KEY")
	GeminiModel  = GetEnv("GEMINI_MODEL", "gemini-3.5-flash")
	ListenPort   = GetEnv("PORT", "8085")
	GithubOwner  = GetEnv("GITHUB_OWNER", "vinhthang")
	GithubRepo   = GetEnv("GITHUB_REPO", "oci")

	WorkspaceDir = "/app/workspace/oci"
	CooldownMins = 5 * time.Minute
)

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
