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
