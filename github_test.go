package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer returns an httptest.Server that routes requests to handler
// based on method + path prefix. It also returns a GitHubClient pointed at it.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *GitHubClient) {
	t.Helper()
	srv := httptest.NewServer(handler)
	client := &GitHubClient{
		BaseURL:    srv.URL,
		Token:      "test-token",
		HTTPClient: srv.Client(),
	}
	return srv, client
}

func TestGetPR(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/owner/repo/pulls/42" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth header = %q, want Bearer test-token", got)
		}
		json.NewEncoder(w).Encode(PR{
			Body: "some body",
			User: struct {
				Login string `json:"login"`
			}{Login: "dependabot[bot]"},
		})
	})
	defer srv.Close()

	pr, err := client.GetPR("owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if pr.Body != "some body" {
		t.Errorf("body = %q, want %q", pr.Body, "some body")
	}
	if pr.User.Login != "dependabot[bot]" {
		t.Errorf("login = %q, want %q", pr.User.Login, "dependabot[bot]")
	}
}

func TestGetPR_NotFound(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	defer srv.Close()

	_, err := client.GetPR("owner", "repo", 999)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to contain 404", err)
	}
}

func TestGetLatestCommit(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/42/commits") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		commits := []Commit{
			{SHA: "aaa", Commit: struct {
				Message string `json:"message"`
			}{Message: "first commit"}},
			{SHA: "bbb", Commit: struct {
				Message string `json:"message"`
			}{Message: "Bump foo from 1.0 to 2.0"}},
		}
		json.NewEncoder(w).Encode(commits)
	})
	defer srv.Close()

	commit, err := client.GetLatestCommit("owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetLatestCommit: %v", err)
	}
	if commit.Commit.Message != "Bump foo from 1.0 to 2.0" {
		t.Errorf("message = %q, want %q", commit.Commit.Message, "Bump foo from 1.0 to 2.0")
	}
}

func TestGetLatestCommit_NoCommits(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Commit{})
	})
	defer srv.Close()

	_, err := client.GetLatestCommit("owner", "repo", 42)
	if err == nil {
		t.Fatal("expected error for empty commits")
	}
}

func TestGetComments(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		comments := []Comment{
			{Body: "## Original PR Description\n\nold body"},
		}
		json.NewEncoder(w).Encode(comments)
	})
	defer srv.Close()

	comments, err := client.GetComments("owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	if !strings.Contains(comments[0].Body, "Original PR Description") {
		t.Error("comment body missing expected content")
	}
}

func TestPostComment(t *testing.T) {
	var gotBody string
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/owner/repo/issues/42/comments" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	err := client.PostComment("owner", "repo", 42, "hello world")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	var payload struct {
		Body string `json:"body"`
	}
	json.Unmarshal([]byte(gotBody), &payload)
	if payload.Body != "hello world" {
		t.Errorf("posted body = %q, want %q", payload.Body, "hello world")
	}
}

func TestUpdatePRBody(t *testing.T) {
	var gotBody string
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/repos/owner/repo/pulls/42" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	err := client.UpdatePRBody("owner", "repo", 42, "new body")
	if err != nil {
		t.Fatalf("UpdatePRBody: %v", err)
	}
	var payload struct {
		Body string `json:"body"`
	}
	json.Unmarshal([]byte(gotBody), &payload)
	if payload.Body != "new body" {
		t.Errorf("patched body = %q, want %q", payload.Body, "new body")
	}
}

func TestParseLinkNext(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{"no next", `<https://api.github.com/foo?page=1>; rel="prev"`, ""},
		{
			"has next",
			`<https://api.github.com/foo?page=1>; rel="prev", <https://api.github.com/foo?page=3>; rel="next"`,
			"https://api.github.com/foo?page=3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLinkNext(tt.header)
			if got != tt.want {
				t.Errorf("parseLinkNext(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// TestHappyPathIntegration exercises the full orchestration logic with a mock
// GitHub API.
func TestHappyPathIntegration(t *testing.T) {
	var commentPosted, bodyUpdated bool
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/10":
			json.NewEncoder(w).Encode(PR{
				Body: "Bumps [foo](https://example.com) from 1.0 to 2.0.\n\n---\n\nChangelog...",
				User: struct {
					Login string `json:"login"`
				}{Login: "dependabot[bot]"},
			})

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/10/commits"):
			json.NewEncoder(w).Encode([]Commit{
				{SHA: "abc", Commit: struct {
					Message string `json:"message"`
				}{Message: "Bump foo from 1.0 to 2.0\n\nSigned-off-by: dependabot[bot] <support@github.com>"}},
			})

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/issues/10/comments"):
			json.NewEncoder(w).Encode([]Comment{})

		case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/10/comments":
			commentPosted = true
			b, _ := io.ReadAll(r.Body)
			var payload struct {
				Body string `json:"body"`
			}
			json.Unmarshal(b, &payload)
			if !strings.HasPrefix(payload.Body, "## Original PR Description") {
				t.Errorf("comment missing header, got: %s", payload.Body[:50])
			}
			w.WriteHeader(http.StatusCreated)

		case r.Method == "PATCH" && r.URL.Path == "/repos/owner/repo/pulls/10":
			bodyUpdated = true
			b, _ := io.ReadAll(r.Body)
			var payload struct {
				Body string `json:"body"`
			}
			json.Unmarshal(b, &payload)
			if payload.Body != "Bump foo from 1.0 to 2.0" {
				t.Errorf("updated body = %q, want commit message without Signed-off-by", payload.Body)
			}
			w.WriteHeader(http.StatusOK)

		default:
			http.Error(w, "unexpected: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
	defer srv.Close()

	err := run(client, "owner", "repo", 10, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !commentPosted {
		t.Error("comment was not posted")
	}
	if !bodyUpdated {
		t.Error("PR body was not updated")
	}
}

func TestNotDependabot(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/10" {
			json.NewEncoder(w).Encode(PR{
				Body: "some body",
				User: struct {
					Login string `json:"login"`
				}{Login: "human-user"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	})
	defer srv.Close()

	err := run(client, "owner", "repo", 10, false)
	if err != nil {
		t.Fatalf("run: %v (expected nil for non-dependabot)", err)
	}
}

func TestEmptyBody(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/10" {
			json.NewEncoder(w).Encode(PR{
				Body: "",
				User: struct {
					Login string `json:"login"`
				}{Login: "dependabot[bot]"},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	})
	defer srv.Close()

	err := run(client, "owner", "repo", 10, false)
	if err != nil {
		t.Fatalf("run: %v (expected nil for empty body)", err)
	}
}

func TestDryRun(t *testing.T) {
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/10":
			json.NewEncoder(w).Encode(PR{
				Body: "old body",
				User: struct {
					Login string `json:"login"`
				}{Login: "dependabot[bot]"},
			})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/10/commits"):
			json.NewEncoder(w).Encode([]Commit{
				{SHA: "abc", Commit: struct {
					Message string `json:"message"`
				}{Message: "Bump bar from 1.0 to 2.0"}},
			})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/issues/10/comments"):
			json.NewEncoder(w).Encode([]Comment{})
		default:
			t.Errorf("unexpected write request in dry-run: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	})
	defer srv.Close()

	err := run(client, "owner", "repo", 10, true)
	if err != nil {
		t.Fatalf("run dry-run: %v", err)
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

func TestIdempotency_SkipsDuplicateComment(t *testing.T) {
	var commentPosted bool
	srv, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/owner/repo/pulls/10":
			json.NewEncoder(w).Encode(PR{
				Body: "old body",
				User: struct {
					Login string `json:"login"`
				}{Login: "dependabot[bot]"},
			})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls/10/commits"):
			json.NewEncoder(w).Encode([]Commit{
				{SHA: "abc", Commit: struct {
					Message string `json:"message"`
				}{Message: "Bump baz from 1.0 to 2.0"}},
			})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/repos/owner/repo/issues/10/comments"):
			// Return a comment that already has the Original PR Description header.
			json.NewEncoder(w).Encode([]Comment{
				{Body: "## Original PR Description\n\nprevious body"},
			})
		case r.Method == "POST" && r.URL.Path == "/repos/owner/repo/issues/10/comments":
			commentPosted = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == "PATCH" && r.URL.Path == "/repos/owner/repo/pulls/10":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	})
	defer srv.Close()

	err := run(client, "owner", "repo", 10, false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if commentPosted {
		t.Error("comment was posted again despite idempotency guard")
	}
}
