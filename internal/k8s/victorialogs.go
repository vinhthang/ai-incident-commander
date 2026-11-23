package k8s

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var victoriaLogsHTTPClient = &http.Client{Timeout: 5 * time.Second}

func sanitizeLogsQLToken(raw string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == ':', r == '/':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(raw))
}

// FetchVictoriaLogsContext queries VictoriaLogs LogsQL for recent trace or service errors.
// Returns an empty string when baseURL is unset, inputs are empty, or the query fails.
func FetchVictoriaLogsContext(ctx context.Context, baseURL, serviceName, traceID string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return ""
	}

	cleanTrace := sanitizeLogsQLToken(traceID)
	cleanSvc := sanitizeLogsQLToken(serviceName)
	var logsQL string
	switch {
	case cleanTrace != "":
		logsQL = fmt.Sprintf(`"%s"`, cleanTrace)
	case cleanSvc != "":
		logsQL = fmt.Sprintf(`"%s" AND (error OR panic OR exception OR status_code:5*)`, cleanSvc)
	default:
		return ""
	}

	if ctx == nil {
		ctx = context.Background()
	}
	qCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	endpoint := fmt.Sprintf("%s/select/logsql/query?query=%s&limit=20", trimmedBase, url.QueryEscape(logsQL))
	req, err := http.NewRequestWithContext(qCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}

	resp, err := victoriaLogsHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var entries []string
	scanner := bufio.NewScanner(io.LimitReader(resp.Body, 64*1024))
	for scanner.Scan() && len(entries) < 20 {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row struct {
			Time string `json:"_time"`
			Msg  string `json:"_msg"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.Msg != "" {
			if row.Time != "" {
				entries = append(entries, fmt.Sprintf("%s %s", row.Time, strings.TrimSpace(row.Msg)))
			} else {
				entries = append(entries, strings.TrimSpace(row.Msg))
			}
		} else {
			entries = append(entries, line)
		}
	}
	if len(entries) == 0 {
		return ""
	}
	return "=== VictoriaLogs Trace Context ===\n" + strings.Join(entries, "\n") + "\n"
}
