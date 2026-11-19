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

var Client *github.Client

func InitClient() {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: config.GithubToken})
	tc := oauth2.NewClient(ctx, ts)
	Client = github.NewClient(tc)
}

func CreateIssue(title, body string, labels []string) (*github.Issue, error) {
	req := &github.IssueRequest{
		Title:  github.String(title),
		Body:   github.String(body),
		Labels: &labels,
	}
	issue, _, err := Client.Issues.Create(context.Background(), config.GithubOwner, config.GithubRepo, req)
	return issue, err
}

func CreatePullRequest(branchName, title, body string) (*github.PullRequest, error) {
	newPR := &github.NewPullRequest{
		Title:               github.String(title),
		Head:                github.String(branchName),
		Base:                github.String("main"),
		Body:                github.String(body),
		MaintainerCanModify: github.Bool(true),
	}
	pr, _, err := Client.PullRequests.Create(context.Background(), config.GithubOwner, config.GithubRepo, newPR)
	return pr, err
}

func GetPullRequestDiff(prNumber int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", config.GithubOwner, config.GithubRepo, prNumber)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "token "+config.GithubToken)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	return string(bodyBytes), err
}

func MergePullRequest(prNumber int) error {
	_, _, err := Client.PullRequests.Merge(context.Background(), config.GithubOwner, config.GithubRepo, prNumber, "Auto-merged by AI Incident Commander Reviewer", nil)
	return err
}

func AddIssueComment(issueNumber int, comment string) error {
	_, _, err := Client.Issues.CreateComment(context.Background(), config.GithubOwner, config.GithubRepo, issueNumber, &github.IssueComment{Body: github.String(comment)})
	return err
}

func AssignIssueOrPR(number int, assignee string) error {
	_, _, err := Client.Issues.AddAssignees(context.Background(), config.GithubOwner, config.GithubRepo, number, []string{assignee})
	return err
}

func CloseIssue(issueNumber int, comment string) error {
	if comment != "" {
		_ = AddIssueComment(issueNumber, comment)
	}
	state := "closed"
	req := &github.IssueRequest{
		State: &state,
	}
	_, _, err := Client.Issues.Edit(context.Background(), config.GithubOwner, config.GithubRepo, issueNumber, req)
	return err
}

func FindOpenIssueByTitle(titlePrefix string) (*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		Labels:      []string{"ai-incident"},
		ListOptions: github.ListOptions{PerPage: 50},
	}
	issues, _, err := Client.Issues.ListByRepo(context.Background(), config.GithubOwner, config.GithubRepo, opts)
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

