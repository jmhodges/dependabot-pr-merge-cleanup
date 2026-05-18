package main

import (
	"context"

	"github.com/google/go-github/v76/github"
)

// ghClient wraps *github.Client with the pagination loops and author
// filtering that run() needs.
type ghClient struct {
	gh *github.Client
}

func (c *ghClient) GetPR(ctx context.Context, owner, repo string, number int) (*github.PullRequest, error) {
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, number)
	return pr, err
}

// LatestDependabotCommit returns the most recent commit authored by
// dependabot[bot] on a pull request, ignoring any commits a human or other bot
// pushed on top (e.g. a maintainer's rebase fixup). GitHub returns commits in
// ascending chronological order and the endpoint does not support reversing,
// so we scan forward across all pages and keep the last dependabot commit we
// see.
func (c *ghClient) LatestDependabotCommit(ctx context.Context, owner, repo string, number int) (*github.RepositoryCommit, error) {
	opt := &github.ListOptions{PerPage: 100}
	var latest *github.RepositoryCommit
	for {
		commits, resp, err := c.gh.PullRequests.ListCommits(ctx, owner, repo, number, opt)
		if err != nil {
			return nil, err
		}
		for _, commit := range commits {
			if commit.GetAuthor().GetLogin() == dependabotLogin {
				latest = commit
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	if latest == nil {
		return nil, errNoDependabotCommits
	}
	return latest, nil
}

func (c *ghClient) ListIssueComments(ctx context.Context, owner, repo string, number int) ([]*github.IssueComment, error) {
	opt := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	var all []*github.IssueComment
	for {
		batch, resp, err := c.gh.Issues.ListComments(ctx, owner, repo, number, opt)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func (c *ghClient) PostComment(ctx context.Context, owner, repo string, number int, body string) error {
	_, _, err := c.gh.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &body})
	return err
}

func (c *ghClient) UpdatePRBody(ctx context.Context, owner, repo string, number int, body string) error {
	_, _, err := c.gh.PullRequests.Edit(ctx, owner, repo, number, &github.PullRequest{Body: &body})
	return err
}
