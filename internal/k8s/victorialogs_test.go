package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchVictoriaLogsContext(t *testing.T) {
	t.Run("queries by sanitized traceID and formats NDJSON entries", func(t *testing.T) {
		var seenQuery string
		var seenPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenPath = r.URL.Path
			seenQuery = r.URL.Query().Get("query")
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"_time":"2026-11-22T12:00:00Z","_msg":"panic in orders handler"}` + "\n" + `{"_time":"2026-11-22T12:00:01Z","_msg":"http 500 returned"}` + "\n"))
		}))
		defer srv.Close()

		got := FetchVictoriaLogsContext(context.Background(), srv.URL, "orders-svc", `4bf92f35"`+"\nAND evil")
		if seenPath != "/select/logsql/query" {
			t.Fatalf("expected /select/logsql/query path, got %q", seenPath)
		}
		if strings.Contains(seenQuery, "\n") {
			t.Fatalf("expected newlines sanitized out of query, got %q", seenQuery)
		}
		if !strings.Contains(seenQuery, "4bf92f35") {
			t.Fatalf("expected traceID in query, got %q", seenQuery)
		}
		for _, want := range []string{
			"=== VictoriaLogs Trace Context ===",
			"2026-11-22T12:00:00Z",
			"panic in orders handler",
			"http 500 returned",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("expected output to contain %q, got:\n%s", want, got)
			}
		}
	})

	t.Run("queries by serviceName when traceID is empty and handles un-timed/plain lines", func(t *testing.T) {
		var seenQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenQuery = r.URL.Query().Get("query")
			_, _ = w.Write([]byte(`{"_msg":"timeout calling db"}` + "\n" + "raw unformatted log line\n"))
		}))
		defer srv.Close()

		got := FetchVictoriaLogsContext(context.Background(), srv.URL, "checkout", "")
		if !strings.Contains(seenQuery, "checkout") {
			t.Fatalf("expected serviceName in LogsQL query, got %q", seenQuery)
		}
		if !strings.Contains(got, "timeout calling db") || !strings.Contains(got, "raw unformatted log line") {
			t.Fatalf("expected both untimed JSON and raw lines, got:\n%s", got)
		}
	})

	t.Run("degrades gracefully on empty URL, empty inputs, and HTTP 500", func(t *testing.T) {
		if got := FetchVictoriaLogsContext(context.Background(), "", "svc", "trace"); got != "" {
			t.Fatalf("expected empty output for empty baseURL, got %q", got)
		}
		if got := FetchVictoriaLogsContext(context.Background(), "http://example.invalid", "", ""); got != "" {
			t.Fatalf("expected empty output for empty service & trace, got %q", got)
		}

		srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srvErr.Close()
		if got := FetchVictoriaLogsContext(context.Background(), srvErr.URL, "svc", "trace"); got != "" {
			t.Fatalf("expected empty output on HTTP 500, got %q", got)
		}
	})
}
