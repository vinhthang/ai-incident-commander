package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gh "github.com/google/go-github/v59/github"
	"vinhthang.dev/ai-incident-commander/internal/config"
)

func TestEvaluateCheckRuns(t *testing.T) {
	tests := []struct {
		name         string
		runs         []*gh.CheckRun
		expectDone   bool
		expectPassed bool
		expectSubstr string
	}{
		{
			name:         "no check runs yet keeps waiting",
			runs:         nil,
			expectDone:   false,
			expectPassed: false,
			expectSubstr: "waiting",
		},
		{
			name: "in-progress check run keeps waiting",
			runs: []*gh.CheckRun{
				{Name: gh.String("build"), Status: gh.String("in_progress")},
			},
			expectDone:   false,
			expectPassed: false,
			expectSubstr: "build",
		},
		{
			name: "success, neutral, and skipped check runs pass",
			runs: []*gh.CheckRun{
				{Name: gh.String("pytest"), Status: gh.String("completed"), Conclusion: gh.String("success")},
				{Name: gh.String("helm"), Status: gh.String("completed"), Conclusion: gh.String("neutral")},
				{Name: gh.String("docs"), Status: gh.String("completed"), Conclusion: gh.String("skipped")},
			},
			expectDone:   true,
			expectPassed: true,
			expectSubstr: "passed",
		},
		{
			name: "failed check run fails gate with summary",
			runs: []*gh.CheckRun{
				{Name: gh.String("pytest"), Status: gh.String("completed"), Conclusion: gh.String("success")},
				{
					Name:       gh.String("helm-lint"),
					Status:     gh.String("completed"),
					Conclusion: gh.String("failure"),
					Output:     &gh.CheckRunOutput{Title: gh.String("Invalid YAML indentation")},
				},
			},
			expectDone:   true,
			expectPassed: false,
			expectSubstr: "helm-lint",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done, passed, summary := EvaluateCheckRuns(tc.runs)
			if done != tc.expectDone || passed != tc.expectPassed {
				t.Fatalf("EvaluateCheckRuns() = (done=%v, passed=%v, summary=%q), want (done=%v, passed=%v)",
					done, passed, summary, tc.expectDone, tc.expectPassed)
			}
			if !strings.Contains(strings.ToLower(summary), strings.ToLower(tc.expectSubstr)) {
				t.Errorf("expected summary %q to contain %q", summary, tc.expectSubstr)
			}
		})
	}
}

func TestWaitForPRChecks(t *testing.T) {
	origClient := Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	defer func() {
		Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
	}()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"

	setupMockServer := func(handler http.HandlerFunc) *httptest.Server {
		srv := httptest.NewServer(handler)
		Client = gh.NewClient(srv.Client())
		baseURL, _ := url.Parse(srv.URL + "/")
		Client.BaseURL = baseURL
		return srv
	}

	t.Run("waits for pending checks then passes on success", func(t *testing.T) {
		var calls atomic.Int32
		srv := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/repos/vinhthang/oci/pulls/42":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"number": 42,
					"head":   map[string]any{"sha": "abc123"},
				})
			case "/repos/vinhthang/oci/commits/abc123/check-runs":
				n := calls.Add(1)
				if n == 1 {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"total_count": 1,
						"check_runs": []map[string]any{
							{"name": "ci", "status": "in_progress"},
						},
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total_count": 1,
					"check_runs": []map[string]any{
						{"name": "ci", "status": "completed", "conclusion": "success"},
					},
				})
			default:
				http.NotFound(w, r)
			}
		})
		defer srv.Close()

		passed, summary, err := WaitForPRChecks(context.Background(), 42, 5*time.Millisecond, 250*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !passed {
			t.Fatalf("expected checks to pass, got summary=%q", summary)
		}
		if calls.Load() < 2 {
			t.Fatalf("expected polling until check completed, calls=%d", calls.Load())
		}
	})

	t.Run("returns false with failure details when check fails", func(t *testing.T) {
		srv := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/repos/vinhthang/oci/pulls/43":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"number": 43,
					"head":   map[string]any{"sha": "def456"},
				})
			case "/repos/vinhthang/oci/commits/def456/check-runs":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total_count": 1,
					"check_runs": []map[string]any{
						{
							"name":       "helm-policy-tests",
							"status":     "completed",
							"conclusion": "failure",
							"output":     map[string]any{"title": "Secret policy violation"},
						},
					},
				})
			default:
				http.NotFound(w, r)
			}
		})
		defer srv.Close()

		passed, summary, err := WaitForPRChecks(context.Background(), 43, 5*time.Millisecond, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error on failed check run: %v", err)
		}
		if passed {
			t.Fatalf("expected passed=false on failed check run")
		}
		if !strings.Contains(summary, "helm-policy-tests") {
			t.Fatalf("expected failure summary to mention check name, got %q", summary)
		}
	})

	t.Run("returns timeout error when checks stay pending", func(t *testing.T) {
		srv := setupMockServer(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/repos/vinhthang/oci/pulls/44":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"number": 44,
					"head":   map[string]any{"sha": "stuck999"},
				})
			case "/repos/vinhthang/oci/commits/stuck999/check-runs":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"total_count": 1,
					"check_runs": []map[string]any{
						{"name": "deploy-ci", "status": "queued"},
					},
				})
			default:
				http.NotFound(w, r)
			}
		})
		defer srv.Close()

		passed, _, err := WaitForPRChecks(context.Background(), 44, 5*time.Millisecond, 30*time.Millisecond)
		if passed {
			t.Fatalf("expected passed=false on timeout")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded on timeout, got %v", err)
		}
	})
}

func TestFindOpenIssueForTarget(t *testing.T) {
	origClient := Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	defer func() {
		Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
	}()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/vinhthang/oci/issues" {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":       336,
					"title":        "[Alert] PodCrashLoopBackOff: system/ai-commander-daily-report",
					"pull_request": map[string]any{"url": "https://api.github.com/repos/vinhthang/oci/pulls/336"},
				},
				{
					"number": 335,
					"title":  "[Alert] PodCrashLoopBackOff: system/ai-commander-daily-report-29918895-t9lz7",
				},
				{
					"number": 340,
					"title":  "[Alert] PodOOMKilled: apps/memos",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	Client.BaseURL = baseURL

	found, err := FindOpenIssueForTarget("system", "ai-commander-daily-report-29918895", "ai-commander-daily-report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.GetNumber() != 335 {
		t.Fatalf("expected open issue #335 matching workload base, got %+v", found)
	}

	none, err := FindOpenIssueForTarget("apps", "vietcalendar", "vietcalendar")
	if err != nil {
		t.Fatalf("unexpected error for unmatched target: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for unmatched target, got issue #%d", none.GetNumber())
	}
}

func TestFindOpenPRForTarget(t *testing.T) {
	origClient := Client
	origOwner := config.GithubOwner
	origRepo := config.GithubRepo
	defer func() {
		Client = origClient
		config.GithubOwner = origOwner
		config.GithubRepo = origRepo
	}()
	config.GithubOwner = "vinhthang"
	config.GithubRepo = "oci"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/repos/vinhthang/oci/pulls" {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number": 100,
					"title":  "Update README for system/ai-commander-daily-report",
					"body":   "Manual docs update",
					"head":   map[string]any{"ref": "feat/docs"},
				},
				{
					"number": 336,
					"title":  "Fix for PodCrashLoopBackOff: system/ai-commander-daily-report-29918895-t9lz7 (Issue #335)",
					"body":   "Automated PR.\n\n**Namespace**: system\n**Workload**: ai-commander-daily-report\n\nCloses #335",
					"head":   map[string]any{"ref": "fix/alert-335-1795133964"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	Client = gh.NewClient(srv.Client())
	baseURL, _ := url.Parse(srv.URL + "/")
	Client.BaseURL = baseURL

	pr, err := FindOpenPRForTarget("system", "ai-commander-daily-report-29918895", "ai-commander-daily-report")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pr == nil || pr.GetNumber() != 336 {
		t.Fatalf("expected open fix PR #336 matching workload base, got %+v", pr)
	}

	none, err := FindOpenPRForTarget("apps", "vietcalendar", "vietcalendar")
	if err != nil {
		t.Fatalf("unexpected error for unmatched target: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for unmatched target, got PR #%d", none.GetNumber())
	}
}
