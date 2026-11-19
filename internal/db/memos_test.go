package db

import (
	"context"
	"strings"
	"testing"
)

func TestFormatDailyMemosNote(t *testing.T) {
	note := FormatDailyMemosNote("2026-11-20", "HEALTHY", "All systems nominal.")
	for _, want := range []string{
		"2026-11-20",
		"HEALTHY",
		"All systems nominal.",
		"#DailyFleetCheck",
		"#AgyReport",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("formatted note missing %q, got:\n%s", want, note)
		}
	}

	if err := InsertMemosReport(context.Background(), "", note, false); err != nil {
		t.Fatalf("expected empty Memos DSN to no-op cleanly, got err=%v", err)
	}
}
