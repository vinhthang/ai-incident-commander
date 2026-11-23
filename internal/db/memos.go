package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// FormatDailyMemosNote wraps the daily fleet audit Markdown report with consistent headers and Memos tags.
func FormatDailyMemosNote(dateStr, status, report string) string {
	return fmt.Sprintf("# 🤖 AI Fleet Health Check (%s) — %s\n\n%s\n\n---\n#DailyFleetCheck #AgyReport",
		dateStr, status, strings.TrimSpace(report))
}

// FormatAppIncidentMemosNote wraps an open application incident diagnosis for Memos delivery.
func FormatAppIncidentMemosNote(serviceName, endpointOrTopic, statusCode, traceID, diagnosis string) string {
	traceVal := strings.TrimSpace(traceID)
	if traceVal == "" {
		traceVal = "N/A"
	}
	endpointVal := strings.TrimSpace(endpointOrTopic)
	if endpointVal == "" {
		endpointVal = "N/A"
	}
	return fmt.Sprintf("# 🚨 App Incident: %s (%s)\n\n- **Endpoint / Topic**: `%s`\n- **Trace ID**: `%s`\n\n%s\n\n---\n#AppIncident #AgyTriage",
		strings.TrimSpace(serviceName), strings.TrimSpace(statusCode), endpointVal, traceVal, strings.TrimSpace(diagnosis))
}

// FormatRemediationMemosNote formats a reactive or proactive auto-remediation result for Memos (#AutoRemediation).
func FormatRemediationMemosNote(source, mode, target, outcome, rootCause, details, issueRef, prRef string) string {
	issueVal := defaultIfBlank(issueRef, "N/A")
	prVal := defaultIfBlank(prRef, "N/A")
	causeVal := defaultIfBlank(rootCause, "N/A")
	detailVal := defaultIfBlank(details, "N/A")
	return fmt.Sprintf(
		"# 🛠️ Auto-Remediation (%s): %s\n\n- **Source**: `%s`\n- **Mode**: `%s`\n- **Outcome**: `%s`\n- **GitHub Issue**: %s\n- **GitHub PR**: %s\n- **Root Cause**: %s\n\n### Execution Summary\n%s\n\n---\n#AutoRemediation #AgyFix",
		strings.TrimSpace(outcome),
		defaultIfBlank(target, "unknown-target"),
		defaultIfBlank(source, "unknown"),
		defaultIfBlank(mode, "unknown"),
		defaultIfBlank(outcome, "unknown"),
		issueVal,
		prVal,
		causeVal,
		detailVal,
	)
}

func defaultIfBlank(val, fallback string) string {
	if trimmed := strings.TrimSpace(val); trimmed != "" {
		return trimmed
	}
	return fallback
}

// InsertMemosReport inserts a private daily fleet report note into Memos PostgreSQL (public.memo).
// If memosDSN is empty, it returns nil safely so dev/test environments without Memos operate normally.
func InsertMemosReport(ctx context.Context, memosDSN, content string, pinned bool) error {
	if strings.TrimSpace(memosDSN) == "" {
		return nil
	}

	conn, err := sql.Open("postgres", memosDSN)
	if err != nil {
		return fmt.Errorf("open memos db: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var rawUID [8]byte
	if _, err := rand.Read(rawUID[:]); err != nil {
		return fmt.Errorf("generate memo uid: %w", err)
	}
	uid := hex.EncodeToString(rawUID[:])
	nowEpoch := time.Now().Unix()

	const query = `
		INSERT INTO memo (uid, creator_id, content, visibility, pinned, created_ts, updated_ts, payload)
		VALUES ($1, 1, $2, 'PRIVATE', $3, $4, $4, '{}'::jsonb)
	`
	if _, err := conn.ExecContext(ctx, query, uid, content, pinned, nowEpoch); err != nil {
		return fmt.Errorf("insert daily report into memos: %w", err)
	}
	return nil
}
