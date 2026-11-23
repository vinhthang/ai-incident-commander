package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	GithubToken        = os.Getenv("GITHUB_TOKEN")
	GeminiAPIKey       = os.Getenv("GEMINI_API_KEY")
	GeminiModel        = GetEnv("GEMINI_MODEL", "gemini-3.5-flash")
	AgyModel           = GetEnv("AGY_MODEL", GetEnv("GEMINI_MODEL", "gemini-3.8-flash-medium"))
	AgyHostAuthDir     = os.Getenv("AGY_HOST_AUTH_DIR")
	WebhookAuthToken   = os.Getenv("WEBHOOK_AUTH_TOKEN")
	ListenPort         = GetEnv("PORT", "8085")
	GithubOwner        = GetEnv("GITHUB_OWNER", "vinhthang")
	GithubRepo         = GetEnv("GITHUB_REPO", "oci")
	SoakDuration       = ParseDurationEnv("SOAK_DURATION", 60*time.Second)
	AutoMerge          = ParseBoolEnv("AUTO_MERGE", false)
	MemosDBDSN         = os.Getenv("MEMOS_DB_DSN")
	VictoriaMetricsURL = GetEnv("VICTORIAMETRICS_URL", "http://victoriametrics.observability.svc.cluster.local:8428")
	VictoriaLogsURL    = GetEnv("VICTORIALOGS_URL", "http://victorialogs.observability.svc.cluster.local:9428")

	EnableRuntimeRemediation   = ParseBoolEnv("ENABLE_RUNTIME_REMEDIATION", true)
	RuntimeProtectedNamespaces = ParseCSVEnv("RUNTIME_PROTECTED_NAMESPACES", []string{"kube-system", "kube-public", "kube-node-lease", "data"})
	RuntimeVerifyTimeout       = ParseDurationEnv("RUNTIME_VERIFY_TIMEOUT", 90*time.Second)
	EnableDailyProactiveFix    = ParseBoolEnv("ENABLE_DAILY_PROACTIVE_FIX", true)
	DailyMaxProactiveFixes     = ParseIntEnv("DAILY_MAX_PROACTIVE_FIXES", 3)

	WorkspaceDir = GetEnv("WORKSPACE_DIR", "/app/workspace/oci")
	CooldownMins = 5 * time.Minute
)

func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// ParseDurationEnv parses a duration env var, returning defaultVal when unset or invalid.
func ParseDurationEnv(key string, defaultVal time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < 0 {
		return defaultVal
	}
	return parsed
}

// ParseBoolEnv parses a boolean env var, returning defaultVal when unset or invalid.
func ParseBoolEnv(key string, defaultVal bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return defaultVal
	}
	return parsed
}

// ParseIntEnv parses a non-negative integer env var, returning defaultVal when unset or invalid.
func ParseIntEnv(key string, defaultVal int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return defaultVal
	}
	return parsed
}

// ParseCSVEnv parses a comma-separated env var into trimmed non-empty lowercase tokens.
func ParseCSVEnv(key string, defaultVal []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		out := make([]string, len(defaultVal))
		copy(out, defaultVal)
		return out
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.ToLower(strings.TrimSpace(part)); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
