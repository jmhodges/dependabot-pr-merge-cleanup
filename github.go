package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

// Comment holds a single issue/PR comment.
type Comment struct {
	Body string `json:"body"`
}

// apiURL builds an API URL by escaping each path segment individually, then
// joining them onto BaseURL. This prevents values like "foo/bar" from
// introducing extra path segments. Callers can add query parameters to the
// returned *url.URL before calling .String().
func (c *GitHubClient) apiURL(segments ...string) (*url.URL, error) {
	escaped := make([]string, len(segments))
	for i, seg := range segments {
		escaped[i] = url.PathEscape(seg)
	}
	s, err := url.JoinPath(c.BaseURL, escaped...)
	if err != nil {
		return nil, err
	}
	return url.Parse(s)
}

// GetPR fetches a pull request by number.
func (c *GitHubClient) GetPR(owner, repo string, number int) (*PR, error) {
	u, err := c.apiURL("repos", owner, repo, "pulls", strconv.Itoa(number))
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}
	body, err := c.get(u.String())
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

// GetLatestDependabotCommit returns the most recent commit authored by
// dependabot[bot] on a pull request, ignoring any commits a human or other
// bot pushed on top (e.g. a maintainer's rebase fixup).
func (c *GitHubClient) GetLatestDependabotCommit(owner, repo string, number int) (*Commit, error) {
	u, err := c.apiURL("repos", owner, repo, "pulls", strconv.Itoa(number), "commits")
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}
	q := u.Query()
	q.Set("per_page", "100")
	u.RawQuery = q.Encode()

	// GitHub returns commits in ascending chronological order and the
	// endpoint does not support reversing. Scan forward across all pages and
	// keep the last dependabot commit we see.
	var latest *Commit
	for next := u.String(); next != ""; {
		body, linkNext, err := c.getWithLink(next)
		if err != nil {
			return nil, fmt.Errorf("get commits: %w", err)
		}

		var commits []Commit
		if err := json.NewDecoder(body).Decode(&commits); err != nil {
			body.Close()
			return nil, fmt.Errorf("decode commits: %w", err)
		}
		body.Close()

		for i := range commits {
			if commits[i].Author.Login == dependabotLogin {
				latest = &commits[i]
			}
		}
		next = linkNext
	}

	if latest == nil {
		return nil, fmt.Errorf("PR has no commits by %s", dependabotLogin)
	}
	return latest, nil
}

// GetComments returns all comments on a PR/issue.
func (c *GitHubClient) GetComments(owner, repo string, number int) ([]Comment, error) {
	u, err := c.apiURL("repos", owner, repo, "issues", strconv.Itoa(number), "comments")
	if err != nil {
		return nil, fmt.Errorf("build URL: %w", err)
	}
	q := u.Query()
	q.Set("per_page", "100")
	u.RawQuery = q.Encode()

	body, err := c.get(u.String())
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
	u, err := c.apiURL("repos", owner, repo, "issues", strconv.Itoa(number), "comments")
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"body": commentBody})
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}
	return c.post(u.String(), string(payload))
}

// UpdatePRBody replaces the body/description of a pull request.
func (c *GitHubClient) UpdatePRBody(owner, repo string, number int, body string) error {
	u, err := c.apiURL("repos", owner, repo, "pulls", strconv.Itoa(number))
	if err != nil {
		return fmt.Errorf("build URL: %w", err)
	}
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	return c.patch(u.String(), string(payload))
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

