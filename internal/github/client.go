package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v59/github"
	"golang.org/x/oauth2"
	"vinhthang.dev/ai-incident-commander/internal/config"
)

var (
	Client     *github.Client
	APIBaseURL = "https://api.github.com"
	HTTPClient = &http.Client{Timeout: 30 * time.Second}
)

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func InitClient() {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: config.GithubToken})
	tc := oauth2.NewClient(ctx, ts)
	Client = github.NewClient(tc)
}

func CreateIssue(ctx context.Context, title, body string, labels []string) (*github.Issue, error) {
	req := &github.IssueRequest{
		Title:  github.String(title),
		Body:   github.String(body),
		Labels: &labels,
	}
	issue, _, err := Client.Issues.Create(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, req)
	return issue, err
}

func CreatePullRequest(ctx context.Context, branchName, title, body string) (*github.PullRequest, error) {
	newPR := &github.NewPullRequest{
		Title:               github.String(title),
		Head:                github.String(branchName),
		Base:                github.String("main"),
		Body:                github.String(body),
		MaintainerCanModify: github.Bool(true),
	}
	pr, _, err := Client.PullRequests.Create(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, newPR)
	return pr, err
}

func GetPullRequestDiff(ctx context.Context, prNumber int) (string, error) {
	base := strings.TrimRight(APIBaseURL, "/")
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", base, config.GithubOwner, config.GithubRepo, prNumber)
	req, err := http.NewRequestWithContext(contextOrBackground(ctx), http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build PR diff request for #%d: %w", prNumber, err)
	}
	if config.GithubToken != "" {
		req.Header.Set("Authorization", "Bearer "+config.GithubToken)
	}
	req.Header.Set("Accept", "application/vnd.github.v3.diff")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if readErr != nil {
		return "", fmt.Errorf("read PR #%d diff body: %w", prNumber, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch PR #%d diff failed: HTTP %d: %s", prNumber, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return string(bodyBytes), nil
}

// IsIssueOpen reports whether the given GitHub issue is currently in the "open" state.
func IsIssueOpen(ctx context.Context, issueNumber int) (bool, error) {
	if Client == nil || issueNumber <= 0 {
		return false, nil
	}
	issue, _, err := Client.Issues.Get(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, issueNumber)
	if err != nil {
		return false, err
	}
	if issue == nil {
		return false, nil
	}
	return strings.EqualFold(issue.GetState(), "open"), nil
}

func MergePullRequest(ctx context.Context, prNumber int) error {
	_, _, err := Client.PullRequests.Merge(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, prNumber, "Auto-merged by AI Incident Commander Reviewer", nil)
	return err
}

func AddIssueComment(ctx context.Context, issueNumber int, comment string) error {
	_, _, err := Client.Issues.CreateComment(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, issueNumber, &github.IssueComment{Body: github.String(comment)})
	return err
}

func AssignIssueOrPR(ctx context.Context, number int, assignee string) error {
	_, _, err := Client.Issues.AddAssignees(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, number, []string{assignee})
	return err
}

func CloseIssue(ctx context.Context, issueNumber int, comment string) error {
	ctx = contextOrBackground(ctx)
	if comment != "" {
		_ = AddIssueComment(ctx, issueNumber, comment)
	}
	state := "closed"
	req := &github.IssueRequest{
		State: &state,
	}
	_, _, err := Client.Issues.Edit(ctx, config.GithubOwner, config.GithubRepo, issueNumber, req)
	return err
}

func FindOpenIssueByTitle(ctx context.Context, titlePrefix string) (*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{"ai-incident"},
		ListOptions: github.ListOptions{PerPage: 50},
	}
	issues, _, err := Client.Issues.ListByRepo(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, opts)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.GetTitle(), titlePrefix) {
			return issue, nil
		}
	}
	return nil, nil
}

func matchesTargetPrefix(haystack, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	lower := strings.ToLower(haystack)
	searchFrom := 0
	for {
		idx := strings.Index(lower[searchFrom:], needle)
		if idx < 0 {
			return false
		}
		endPos := searchFrom + idx + len(needle)
		if endPos == len(lower) {
			return true
		}
		switch lower[endPos] {
		case '-', ':', '/', ' ', ')', ',', '\n', '\r', '`':
			return true
		}
		searchFrom += idx + 1
	}
}

func targetNeedles(namespace, target, workloadBase string) []string {
	var raw []string
	if namespace != "" {
		if target != "" {
			raw = append(raw, namespace+"/"+target)
		}
		if workloadBase != "" && workloadBase != target {
			raw = append(raw, namespace+"/"+workloadBase)
		}
	} else {
		if target != "" {
			raw = append(raw, target)
		}
		if workloadBase != "" && workloadBase != target {
			raw = append(raw, workloadBase)
		}
	}
	return raw
}

// FindOpenIssueForTarget returns an open ai-incident GitHub Issue referencing the same
// namespace/target or normalized workload base (ignoring PRs), preventing duplicate issues across alerts.
func FindOpenIssueForTarget(ctx context.Context, namespace, target, workloadBase string) (*github.Issue, error) {
	if Client == nil {
		return nil, nil
	}
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{"ai-incident"},
		ListOptions: github.ListOptions{PerPage: 50},
	}
	issues, _, err := Client.Issues.ListByRepo(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, opts)
	if err != nil {
		return nil, err
	}

	needles := targetNeedles(namespace, target, workloadBase)
	for _, issue := range issues {
		if issue == nil || issue.IsPullRequest() {
			continue
		}
		title := issue.GetTitle()
		for _, needle := range needles {
			if matchesTargetPrefix(title, needle) {
				return issue, nil
			}
		}
	}
	return nil, nil
}

// FindOpenPRForTarget returns an open autonomous fix PR (head ref fix/alert-*) targeting the same
// namespace/target or workload base so subsequent alerts do not create redundant fix PRs.
func FindOpenPRForTarget(ctx context.Context, namespace, target, workloadBase string) (*github.PullRequest, error) {
	if Client == nil {
		return nil, nil
	}
	opts := &github.PullRequestListOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 50},
	}
	prs, _, err := Client.PullRequests.List(contextOrBackground(ctx), config.GithubOwner, config.GithubRepo, opts)
	if err != nil {
		return nil, err
	}

	needles := targetNeedles(namespace, target, workloadBase)
	if workloadBase != "" {
		needles = append(needles, "workload: "+workloadBase, "**workload**: "+workloadBase)
	}

	for _, pr := range prs {
		if pr == nil || !strings.HasPrefix(pr.GetHead().GetRef(), "fix/alert-") {
			continue
		}
		title := pr.GetTitle()
		body := pr.GetBody()
		for _, needle := range needles {
			if matchesTargetPrefix(title, needle) || matchesTargetPrefix(body, needle) {
				return pr, nil
			}
		}
	}
	return nil, nil
}

// EvaluateCheckRuns inspects a snapshot of GitHub check runs on a PR head commit.
// It returns done=true once checks finish or fail, passed=true only if >=1 run exists
// and every completed check concluded with success, neutral, or skipped.
func EvaluateCheckRuns(runs []*github.CheckRun) (done bool, passed bool, summary string) {
	if len(runs) == 0 {
		return false, false, "waiting for CI check runs to start"
	}

	var pending []string
	var failures []string

	for _, cr := range runs {
		if cr == nil {
			continue
		}
		name := cr.GetName()
		if name == "" {
			name = "unnamed-check"
		}
		if cr.GetStatus() != "completed" {
			pending = append(pending, fmt.Sprintf("%s (%s)", name, cr.GetStatus()))
			continue
		}

		conclusion := cr.GetConclusion()
		switch conclusion {
		case "success", "neutral", "skipped":
		default:
			detail := ""
			if out := cr.GetOutput(); out != nil {
				if out.GetTitle() != "" {
					detail = out.GetTitle()
				} else if out.GetSummary() != "" {
					detail = out.GetSummary()
				}
			}
			if detail != "" {
				failures = append(failures, fmt.Sprintf("%s (%s: %s)", name, conclusion, detail))
			} else {
				failures = append(failures, fmt.Sprintf("%s (%s)", name, conclusion))
			}
		}
	}

	if len(failures) > 0 {
		return true, false, fmt.Sprintf("CI checks failed: %s", strings.Join(failures, "; "))
	}
	if len(pending) > 0 {
		return false, false, fmt.Sprintf("waiting for CI check runs: %s", strings.Join(pending, ", "))
	}
	return true, true, fmt.Sprintf("all %d CI check runs passed", len(runs))
}

// WaitForPRChecks polls GitHub Check Runs for the PR head commit until all checks
// pass, any check fails, or timeout elapses.
func WaitForPRChecks(ctx context.Context, prNumber int, pollInterval, timeout time.Duration) (bool, string, error) {
	if pollInterval <= 0 {
		pollInterval = 15 * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pr, _, err := Client.PullRequests.Get(waitCtx, config.GithubOwner, config.GithubRepo, prNumber)
	if err != nil {
		return false, "", fmt.Errorf("fetch PR #%d: %w", prNumber, err)
	}
	headSHA := pr.GetHead().GetSHA()
	if headSHA == "" {
		return false, "", fmt.Errorf("PR #%d has empty head SHA", prNumber)
	}

	lastSummary := "waiting for CI check runs to start"
	for {
		res, _, err := Client.Checks.ListCheckRunsForRef(waitCtx, config.GithubOwner, config.GithubRepo, headSHA, &github.ListCheckRunsOptions{
			ListOptions: github.ListOptions{PerPage: 100},
		})
		if err != nil {
			return false, lastSummary, fmt.Errorf("list check runs for PR #%d (%s): %w", prNumber, headSHA, err)
		}

		done, passed, summary := EvaluateCheckRuns(res.CheckRuns)
		lastSummary = summary
		if done {
			return passed, summary, nil
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return false, lastSummary, waitCtx.Err()
		case <-timer.C:
		}
	}
}
