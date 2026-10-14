package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

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

