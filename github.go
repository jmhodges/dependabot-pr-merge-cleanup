package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// GitHubClient wraps the HTTP client and configuration needed to talk to the
// GitHub REST API.
type GitHubClient struct {
	BaseURL    string // e.g. "https://api.github.com" (no trailing slash)
	Token      string
	HTTPClient *http.Client
}

// PR holds the subset of pull-request fields we care about.
type PR struct {
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// Commit holds a single commit returned by the PR-commits endpoint.
type Commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

// Comment holds a single issue/PR comment.
type Comment struct {
	Body string `json:"body"`
}

// GetPR fetches a pull request by number.
func (c *GitHubClient) GetPR(owner, repo string, number int) (*PR, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.BaseURL, owner, repo, number)
	body, err := c.get(url)
	if err != nil {
		return nil, fmt.Errorf("get PR: %w", err)
	}
	defer body.Close()

	var pr PR
	if err := json.NewDecoder(body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("decode PR: %w", err)
	}
	return &pr, nil
}

// GetLatestCommit returns the most recent commit on a pull request.
func (c *GitHubClient) GetLatestCommit(owner, repo string, number int) (*Commit, error) {
	// GitHub returns commits in chronological order. Fetch the last page to
	// get the most recent commit. For dependabot PRs this is almost always a
	// single page, but we handle pagination to be safe.
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/commits?per_page=100", c.BaseURL, owner, repo, number)

	var latest *Commit
	for url != "" {
		body, linkNext, err := c.getWithLink(url)
		if err != nil {
			return nil, fmt.Errorf("get commits: %w", err)
		}

		var commits []Commit
		if err := json.NewDecoder(body).Decode(&commits); err != nil {
			body.Close()
			return nil, fmt.Errorf("decode commits: %w", err)
		}
		body.Close()

		if len(commits) > 0 {
			latest = &commits[len(commits)-1]
		}
		url = linkNext
	}

	if latest == nil {
		return nil, fmt.Errorf("PR has no commits")
	}
	return latest, nil
}

// GetComments returns all comments on a PR/issue.
func (c *GitHubClient) GetComments(owner, repo string, number int) ([]Comment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100", c.BaseURL, owner, repo, number)
	body, err := c.get(url)
	if err != nil {
		return nil, fmt.Errorf("get comments: %w", err)
	}
	defer body.Close()

	var comments []Comment
	if err := json.NewDecoder(body).Decode(&comments); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	return comments, nil
}

// PostComment creates a new comment on a PR/issue.
func (c *GitHubClient) PostComment(owner, repo string, number int, commentBody string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.BaseURL, owner, repo, number)
	payload := fmt.Sprintf(`{"body":%s}`, jsonString(commentBody))
	return c.post(url, payload)
}

// UpdatePRBody replaces the body/description of a pull request.
func (c *GitHubClient) UpdatePRBody(owner, repo string, number int, body string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.BaseURL, owner, repo, number)
	payload := fmt.Sprintf(`{"body":%s}`, jsonString(body))
	return c.patch(url, payload)
}

// --- HTTP helpers ---

func (c *GitHubClient) newRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *GitHubClient) get(url string) (io.ReadCloser, error) {
	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, nil
}

// getWithLink performs a GET and also parses the Link header to return the
// "next" URL for pagination (empty string if none).
func (c *GitHubClient) getWithLink(url string) (io.ReadCloser, string, error) {
	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return resp.Body, parseLinkNext(resp.Header.Get("Link")), nil
}

func (c *GitHubClient) post(url, payload string) error {
	req, err := c.newRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func (c *GitHubClient) patch(url, payload string) error {
	req, err := c.newRequest("PATCH", url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// parseLinkNext extracts the URL for rel="next" from a GitHub Link header.
// Returns "" if there is no next page.
func parseLinkNext(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, `rel="next"`) {
			start := strings.Index(part, "<")
			end := strings.Index(part, ">")
			if start >= 0 && end > start {
				return part[start+1 : end]
			}
		}
	}
	return ""
}

// jsonString returns s as a JSON-encoded string literal (with escaping).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
