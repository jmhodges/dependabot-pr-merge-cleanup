package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/go-github/v76/github"
)

const (
	commentHeader   = "## Original PR Description"
	dependabotLogin = "dependabot[bot]"
)

// errNoDependabotCommits is returned when a PR has no commits authored by
// dependabot (sentinel so tests can match exactly without string comparisons).
var errNoDependabotCommits = errors.New("PR has no commits by " + dependabotLogin)

func main() {
	repo := flag.String("repo", "", "owner/repo (or set GITHUB_REPOSITORY)")
	prNum := flag.Int("pr", 0, "pull request number (or set PR_NUMBER)")
	dryRun := flag.Bool("dry-run", false, "print what would happen without making changes")
	flag.Parse()

	if *repo == "" {
		*repo = os.Getenv("GITHUB_REPOSITORY")
	}
	if *prNum == 0 {
		if v := os.Getenv("PR_NUMBER"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: PR_NUMBER is not a valid integer: %s\n", v)
				os.Exit(1)
			}
			*prNum = n
		}
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: GITHUB_TOKEN environment variable is required")
		os.Exit(1)
	}
	if *repo == "" {
		fmt.Fprintln(os.Stderr, "error: -repo or GITHUB_REPOSITORY is required")
		os.Exit(1)
	}
	if *prNum == 0 {
		fmt.Fprintln(os.Stderr, "error: -pr or PR_NUMBER is required")
		os.Exit(1)
	}

	parts := strings.SplitN(*repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Fprintf(os.Stderr, "error: -repo must be in owner/repo format, got %q\n", *repo)
		os.Exit(1)
	}
	owner, repoName := parts[0], parts[1]

	client := &ghClient{gh: github.NewClient(nil).WithAuthToken(token)}

	if err := run(context.Background(), client, owner, repoName, *prNum, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run contains the core orchestration logic, separated from main() so it can
// be tested with a ghClient pointed at httptest.NewServer.
func run(ctx context.Context, client *ghClient, owner, repo string, prNumber int, dryRun bool) error {
	pr, err := client.GetPR(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR #%d: %w", prNumber, err)
	}

	if pr.GetUser().GetLogin() != dependabotLogin {
		fmt.Printf("PR #%d is by %s, not dependabot — skipping.\n", prNumber, pr.GetUser().GetLogin())
		return nil
	}

	if strings.TrimSpace(pr.GetBody()) == "" {
		fmt.Printf("PR #%d has an empty body — nothing to preserve.\n", prNumber)
		return nil
	}

	commit, err := client.LatestDependabotCommit(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching commits for PR #%d: %w", prNumber, err)
	}
	newBody := stripSignedOffBy(commit.GetCommit().GetMessage())

	comments, err := client.ListIssueComments(ctx, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching comments for PR #%d: %w", prNumber, err)
	}
	alreadyPosted := false
	for _, c := range comments {
		if strings.HasPrefix(c.GetBody(), commentHeader) {
			alreadyPosted = true
			break
		}
	}

	if dryRun {
		fmt.Println("=== DRY RUN ===")
		fmt.Printf("PR #%d by %s\n\n", prNumber, pr.GetUser().GetLogin())
		if alreadyPosted {
			fmt.Println("Comment with original description already exists — would skip posting.")
		} else {
			fmt.Println("--- Would post comment ---")
			fmt.Println(commentHeader + "\n\n" + pr.GetBody())
			fmt.Println("--- End comment ---")
		}
		fmt.Println()
		fmt.Println("--- Would update PR body to ---")
		fmt.Println(newBody)
		fmt.Println("--- End body ---")
		return nil
	}

	if !alreadyPosted {
		commentBody := commentHeader + "\n\n" + pr.GetBody()
		if err := client.PostComment(ctx, owner, repo, prNumber, commentBody); err != nil {
			return fmt.Errorf("posting comment on PR #%d: %w", prNumber, err)
		}
		fmt.Printf("Posted original description as comment on PR #%d.\n", prNumber)
	} else {
		fmt.Printf("Original description comment already exists on PR #%d — skipped.\n", prNumber)
	}

	if err := client.UpdatePRBody(ctx, owner, repo, prNumber, newBody); err != nil {
		return fmt.Errorf("updating body of PR #%d: %w", prNumber, err)
	}
	fmt.Printf("Updated PR #%d body to latest commit message.\n", prNumber)

	return nil
}

// stripSignedOffBy removes "Signed-off-by:" trailer lines from the end of a
// commit message so they don't get duplicated when GitHub includes the PR body
// in the merge commit.
func stripSignedOffBy(msg string) string {
	lines := strings.Split(msg, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "Signed-off-by:") {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
