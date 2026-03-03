package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

const commentHeader = "## Original PR Description"

func main() {
	repo := flag.String("repo", "", "owner/repo (or set GITHUB_REPOSITORY)")
	prNum := flag.Int("pr", 0, "pull request number (or set PR_NUMBER)")
	dryRun := flag.Bool("dry-run", false, "print what would happen without making changes")
	flag.Parse()

	// Resolve flags from env fallbacks.
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

	// Validate required inputs.
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

	client := &GitHubClient{
		BaseURL:    "https://api.github.com",
		Token:      token,
		HTTPClient: http.DefaultClient,
	}

	if err := run(client, owner, repoName, *prNum, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// run contains the core orchestration logic, separated from main() so it can
// be tested with a mock GitHubClient.
func run(client *GitHubClient, owner, repo string, prNumber int, dryRun bool) error {
	// 1. Fetch the PR.
	pr, err := client.GetPR(owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR #%d: %w", prNumber, err)
	}

	// 2. Only operate on dependabot PRs.
	if pr.User.Login != "dependabot[bot]" {
		fmt.Printf("PR #%d is by %s, not dependabot — skipping.\n", prNumber, pr.User.Login)
		return nil
	}

	// 3. Nothing to do if the body is already empty.
	if strings.TrimSpace(pr.Body) == "" {
		fmt.Printf("PR #%d has an empty body — nothing to preserve.\n", prNumber)
		return nil
	}

	// 4. Get the latest commit message to use as the new body.
	commit, err := client.GetLatestCommit(owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching commits for PR #%d: %w", prNumber, err)
	}
	newBody := stripSignedOffBy(commit.Commit.Message)

	// 5. Check idempotency: has the original description already been posted?
	comments, err := client.GetComments(owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching comments for PR #%d: %w", prNumber, err)
	}
	alreadyPosted := false
	for _, c := range comments {
		if strings.HasPrefix(c.Body, commentHeader) {
			alreadyPosted = true
			break
		}
	}

	if dryRun {
		fmt.Println("=== DRY RUN ===")
		fmt.Printf("PR #%d by %s\n\n", prNumber, pr.User.Login)
		if alreadyPosted {
			fmt.Println("Comment with original description already exists — would skip posting.")
		} else {
			fmt.Println("--- Would post comment ---")
			fmt.Println(commentHeader + "\n\n" + pr.Body)
			fmt.Println("--- End comment ---")
		}
		fmt.Println()
		fmt.Println("--- Would update PR body to ---")
		fmt.Println(newBody)
		fmt.Println("--- End body ---")
		return nil
	}

	// 6. Post the original description as a comment (if not already done).
	if !alreadyPosted {
		commentBody := commentHeader + "\n\n" + pr.Body
		if err := client.PostComment(owner, repo, prNumber, commentBody); err != nil {
			return fmt.Errorf("posting comment on PR #%d: %w", prNumber, err)
		}
		fmt.Printf("Posted original description as comment on PR #%d.\n", prNumber)
	} else {
		fmt.Printf("Original description comment already exists on PR #%d — skipped.\n", prNumber)
	}

	// 7. Update the PR body to the latest commit message.
	if err := client.UpdatePRBody(owner, repo, prNumber, newBody); err != nil {
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
	// Trim trailing blank lines first.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Remove trailing Signed-off-by lines.
	for len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "Signed-off-by:") {
		lines = lines[:len(lines)-1]
	}
	// Trim any blank lines that preceded the trailers.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
