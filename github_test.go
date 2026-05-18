package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v76/github"
)

// newTestClient returns an httptest.Server and a ghClient whose go-github
// instance is pointed at it.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *ghClient) {
	t.Helper()
	srv := httptest.NewServer(handler)
	gh := github.NewClient(srv.Client()).WithAuthToken("test-token")
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	gh.BaseURL = u
	gh.UploadURL = u
	return srv, &ghClient{gh: gh}
}

// dependabotCommit builds a RepositoryCommit fixture authored by dependabot.
func dependabotCommit(sha, msg string) *github.RepositoryCommit {
	return commitBy(sha, msg, dependabotLogin)
}

// commitBy builds a RepositoryCommit fixture with the given author login.
func commitBy(sha, msg, login string) *github.RepositoryCommit {
	return &github.RepositoryCommit{
		SHA:    github.Ptr(sha),
		Commit: &github.Commit{Message: github.Ptr(msg)},
		Author: &github.User{Login: github.Ptr(login)},
	}
}

func TestGhClient_GetPR(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/owner/repo/pulls/42" {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth header = %q, want Bearer test-token", got)
		}
		json.NewEncoder(w).Encode(&github.PullRequest{
			Body: github.Ptr("some body"),
			User: &github.User{Login: github.Ptr("dependabot[bot]")},
		})
	})
	defer srv.Close()

	pr, err := client.GetPR(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if pr.GetBody() != "some body" {
		t.Errorf("body = %q, want %q", pr.GetBody(), "some body")
	}
	if pr.GetUser().GetLogin() != "dependabot[bot]" {
		t.Errorf("login = %q, want %q", pr.GetUser().GetLogin(), "dependabot[bot]")
	}
}

func TestGhClient_GetPR_NotFound(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	defer srv.Close()

	_, err := client.GetPR(context.Background(), "owner", "repo", 999)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to contain 404", err)
	}
}

func TestGhClient_LatestDependabotCommit(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/42/commits") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode([]*github.RepositoryCommit{
			dependabotCommit("aaa", "first commit"),
			dependabotCommit("bbb", "Bump foo from 1.0 to 2.0"),
		})
	})
	defer srv.Close()

	commit, err := client.LatestDependabotCommit(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("LatestDependabotCommit: %v", err)
	}
	if got := commit.GetCommit().GetMessage(); got != "Bump foo from 1.0 to 2.0" {
		t.Errorf("message = %q, want %q", got, "Bump foo from 1.0 to 2.0")
	}
}

// A maintainer's rebase fixup commit on top of a dependabot PR must not
// shadow the underlying dependabot commit.
func TestGhClient_LatestDependabotCommit_SkipsNonDependabot(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.RepositoryCommit{
			dependabotCommit("aaa", "Bump foo from 1.0 to 2.0"),
			commitBy("bbb", "fix lint", "human-user"),
		})
	})
	defer srv.Close()

	commit, err := client.LatestDependabotCommit(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("LatestDependabotCommit: %v", err)
	}
	if commit.GetSHA() != "aaa" {
		t.Errorf("sha = %q, want aaa (the dependabot commit)", commit.GetSHA())
	}
}

// The dependabot commit may live on an earlier page than the non-dependabot
// commits that follow it; the filter must keep working across pages.
func TestGhClient_LatestDependabotCommit_PaginationFiltersAcrossPages(t *testing.T) {
	var srvURL string
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/owner/repo/pulls/42/commits?page=2>; rel="next"`, srvURL))
			json.NewEncoder(w).Encode([]*github.RepositoryCommit{
				dependabotCommit("aaa", "Bump foo from 1.0 to 2.0"),
			})
		case "2":
			json.NewEncoder(w).Encode([]*github.RepositoryCommit{
				commitBy("bbb", "fix lint", "human-user"),
			})
		default:
			http.Error(w, "unexpected page "+page, http.StatusBadRequest)
		}
	})
	srvURL = srv.URL
	defer srv.Close()

	commit, err := client.LatestDependabotCommit(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("LatestDependabotCommit: %v", err)
	}
	if commit.GetSHA() != "aaa" {
		t.Errorf("sha = %q, want aaa", commit.GetSHA())
	}
}

func TestGhClient_LatestDependabotCommit_NoCommits(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.RepositoryCommit{})
	})
	defer srv.Close()

	_, err := client.LatestDependabotCommit(context.Background(), "owner", "repo", 42)
	if err == nil {
		t.Fatal("expected error for empty commits")
	}
	if err != errNoDependabotCommits {
		t.Errorf("err = %v, want errNoDependabotCommits", err)
	}
}

func TestGhClient_LatestDependabotCommit_NoDependabotCommits(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*github.RepositoryCommit{
			commitBy("aaa", "manual change", "human-user"),
		})
	})
	defer srv.Close()

	_, err := client.LatestDependabotCommit(context.Background(), "owner", "repo", 42)
	if err == nil {
		t.Fatal("expected error when no commits are by dependabot")
	}
	if err != errNoDependabotCommits {
		t.Errorf("err = %v, want errNoDependabotCommits", err)
	}
}

func TestGhClient_ListIssueComments(t *testing.T) {
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		json.NewEncoder(w).Encode([]*github.IssueComment{
			{Body: github.Ptr("## Original PR Description\n\nold body")},
		})
	})
	defer srv.Close()

	comments, err := client.ListIssueComments(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if !strings.Contains(comments[0].GetBody(), "Original PR Description") {
		t.Error("comment body missing expected content")
	}
}

func TestGhClient_ListIssueComments_Paginated(t *testing.T) {
	var srvURL string
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/owner/repo/issues/42/comments?page=2>; rel="next"`, srvURL))
			json.NewEncoder(w).Encode([]*github.IssueComment{
				{Body: github.Ptr("first")},
			})
		case "2":
			json.NewEncoder(w).Encode([]*github.IssueComment{
				{Body: github.Ptr("## Original PR Description\n\nstashed")},
			})
		default:
			http.Error(w, "unexpected page="+page, http.StatusBadRequest)
		}
	})
	srvURL = srv.URL
	defer srv.Close()

	comments, err := client.ListIssueComments(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2 across pages", len(comments))
	}
	if !strings.HasPrefix(comments[1].GetBody(), "## Original PR Description") {
		t.Errorf("second-page comment lost: %q", comments[1].GetBody())
	}
}

func TestGhClient_PostComment(t *testing.T) {
	var gotBody string
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/owner/repo/issues/42/comments" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("{}"))
	})
	defer srv.Close()

	err := client.PostComment(context.Background(), "owner", "repo", 42, "hello world")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	var payload struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Body != "hello world" {
		t.Errorf("posted body = %q, want %q", payload.Body, "hello world")
	}
}

func TestGhClient_UpdatePRBody(t *testing.T) {
	var gotBody string
	srv, client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/repos/owner/repo/pulls/42" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}"))
	})
	defer srv.Close()

	err := client.UpdatePRBody(context.Background(), "owner", "repo", 42, "new body")
	if err != nil {
		t.Fatalf("UpdatePRBody: %v", err)
	}
	var payload struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Body != "new body" {
		t.Errorf("patched body = %q, want %q", payload.Body, "new body")
	}
}

// --- run() orchestration tests drive a real ghClient against httptest. ---

// runFixture serves the standard PR/commits/comments GET endpoints from the
// configured fixtures, captures the bodies of mutating requests, and fails
// the test on anything unexpected.
type runFixture struct {
	pr       *github.PullRequest
	commits  []*github.RepositoryCommit
	comments []*github.IssueComment

	commentPosted bool
	postedBody    string
	bodyUpdated   bool
	updatedBody   string
}

func (f *runFixture) handler(t *testing.T, owner, repo string, number int) http.HandlerFunc {
	t.Helper()
	prPath := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	commitsPath := prPath + "/commits"
	commentsPath := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == prPath:
			json.NewEncoder(w).Encode(f.pr)
		case r.Method == "GET" && r.URL.Path == commitsPath:
			json.NewEncoder(w).Encode(f.commits)
		case r.Method == "GET" && r.URL.Path == commentsPath:
			json.NewEncoder(w).Encode(f.comments)
		case r.Method == "POST" && r.URL.Path == commentsPath:
			f.commentPosted = true
			var p struct {
				Body string `json:"body"`
			}
			json.NewDecoder(r.Body).Decode(&p)
			f.postedBody = p.Body
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{}"))
		case r.Method == "PATCH" && r.URL.Path == prPath:
			f.bodyUpdated = true
			var p struct {
				Body string `json:"body"`
			}
			json.NewDecoder(r.Body).Decode(&p)
			f.updatedBody = p.Body
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{}"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}
}

func TestHappyPathIntegration(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr("Bumps [foo](https://example.com) from 1.0 to 2.0.\n\n---\n\nChangelog..."),
			User: &github.User{Login: github.Ptr(dependabotLogin)},
		},
		commits: []*github.RepositoryCommit{
			dependabotCommit("abc", "Bump foo from 1.0 to 2.0\n\nSigned-off-by: dependabot[bot] <support@github.com>"),
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !f.commentPosted {
		t.Error("comment was not posted")
	}
	if !strings.HasPrefix(f.postedBody, "## Original PR Description") {
		t.Errorf("comment missing header, got: %s", f.postedBody)
	}
	if !f.bodyUpdated {
		t.Error("PR body was not updated")
	}
	if f.updatedBody != "Bump foo from 1.0 to 2.0" {
		t.Errorf("updated body = %q, want commit message without Signed-off-by", f.updatedBody)
	}
}

// A maintainer's fixup commit sitting on top of a dependabot commit must not
// become the new PR body. This goes through the full run() flow, exercising
// LatestDependabotCommit's author filter end-to-end.
func TestHappyPath_IgnoresNonDependabotFixup(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr("Bumps foo from 1.0 to 2.0."),
			User: &github.User{Login: github.Ptr(dependabotLogin)},
		},
		commits: []*github.RepositoryCommit{
			dependabotCommit("aaa", "Bump foo from 1.0 to 2.0"),
			commitBy("bbb", "fix lint", "human-user"),
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.updatedBody != "Bump foo from 1.0 to 2.0" {
		t.Errorf("updated body = %q, want the dependabot commit message (not the human fixup)", f.updatedBody)
	}
}

func TestNotDependabot(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr("some body"),
			User: &github.User{Login: github.Ptr("human-user")},
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.commentPosted || f.bodyUpdated {
		t.Error("must not modify non-dependabot PRs")
	}
}

func TestEmptyBody(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr(""),
			User: &github.User{Login: github.Ptr(dependabotLogin)},
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.commentPosted || f.bodyUpdated {
		t.Error("must not modify PRs with empty bodies")
	}
}

func TestDryRun(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr("old body"),
			User: &github.User{Login: github.Ptr(dependabotLogin)},
		},
		commits: []*github.RepositoryCommit{
			dependabotCommit("abc", "Bump bar from 1.0 to 2.0"),
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, true); err != nil {
		t.Fatalf("run dry-run: %v", err)
	}
	if f.commentPosted || f.bodyUpdated {
		t.Error("dry-run must not call mutating endpoints")
	}
}

func TestIdempotency_SkipsDuplicateComment(t *testing.T) {
	f := &runFixture{
		pr: &github.PullRequest{
			Body: github.Ptr("old body"),
			User: &github.User{Login: github.Ptr(dependabotLogin)},
		},
		commits: []*github.RepositoryCommit{
			dependabotCommit("abc", "Bump baz from 1.0 to 2.0"),
		},
		comments: []*github.IssueComment{
			{Body: github.Ptr("## Original PR Description\n\nprevious body")},
		},
	}
	srv, client := newTestClient(t, f.handler(t, "owner", "repo", 10))
	defer srv.Close()

	if err := run(context.Background(), client, "owner", "repo", 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if f.commentPosted {
		t.Error("comment was posted again despite idempotency guard")
	}
	if !f.bodyUpdated {
		t.Error("PR body should still be updated even when comment already exists")
	}
}

func TestStripSignedOffBy(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"no trailer",
			"Bump foo from 1.0 to 2.0",
			"Bump foo from 1.0 to 2.0",
		},
		{
			"single trailer",
			"Bump foo from 1.0 to 2.0\n\nSigned-off-by: dependabot[bot] <support@github.com>",
			"Bump foo from 1.0 to 2.0",
		},
		{
			"trailing newline after trailer",
			"Bump foo from 1.0 to 2.0\n\nSigned-off-by: dependabot[bot] <support@github.com>\n",
			"Bump foo from 1.0 to 2.0",
		},
		{
			"multiple trailers",
			"Bump foo from 1.0 to 2.0\n\nSigned-off-by: dependabot[bot] <support@github.com>\nSigned-off-by: someone <else@example.com>",
			"Bump foo from 1.0 to 2.0",
		},
		{
			"preserves body content above trailers",
			"Bump foo from 1.0 to 2.0\n\nSome details here.\n\nSigned-off-by: dependabot[bot] <support@github.com>",
			"Bump foo from 1.0 to 2.0\n\nSome details here.",
		},
		{
			"empty message",
			"",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripSignedOffBy(tt.in)
			if got != tt.want {
				t.Errorf("stripSignedOffBy(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
