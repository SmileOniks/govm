package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newServer(t *testing.T, status int, body string, seen *http.Request) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r.Clone(context.Background())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestLatestReleaseReturnsTag(t *testing.T) {
	var seen http.Request
	server := newServer(t, http.StatusOK, `{"tag_name":"v0.2.5","draft":false,"prerelease":false}`, &seen)

	client := NewClient(http.DefaultClient, server.URL, "SmileOniks/govm", "govm/0.2.4")
	tag, err := client.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("LatestRelease() error = %v", err)
	}
	if tag != "v0.2.5" {
		t.Fatalf("LatestRelease() = %q, want v0.2.5", tag)
	}
	if seen.URL.Path != "/repos/SmileOniks/govm/releases/latest" {
		t.Fatalf("request path = %q", seen.URL.Path)
	}
	if got := seen.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := seen.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
		t.Fatalf("X-GitHub-Api-Version = %q", got)
	}
	if got := seen.Header.Get("User-Agent"); got != "govm/0.2.4" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestLatestReleaseRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "rate limited", status: http.StatusForbidden, body: `{"message":"API rate limit exceeded"}`, want: "unexpected status"},
		{name: "not found", status: http.StatusNotFound, body: `{"message":"Not Found"}`, want: "unexpected status"},
		{name: "invalid JSON", status: http.StatusOK, body: `{"tag_name":`, want: "fetch latest release"},
		{name: "empty tag", status: http.StatusOK, body: `{"tag_name":"  "}`, want: "empty tag_name"},
		{name: "prerelease flagged", status: http.StatusOK, body: `{"tag_name":"v0.3.0-rc1","prerelease":true}`, want: "not a stable release"},
		{name: "draft flagged", status: http.StatusOK, body: `{"tag_name":"v0.3.0","draft":true}`, want: "not a stable release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newServer(t, tt.status, tt.body, nil)
			client := NewClient(http.DefaultClient, server.URL, "owner/repo", "govm/test")
			tag, err := client.LatestRelease(context.Background())
			if err == nil {
				t.Fatalf("LatestRelease() = %q, want error", tag)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LatestRelease() error = %q, want containing %q", err, tt.want)
			}
		})
	}
}

type failingDoer struct{ err error }

func (f failingDoer) Do(*http.Request) (*http.Response, error) { return nil, f.err }

func TestLatestReleaseWrapsTransportError(t *testing.T) {
	boom := errors.New("dial tcp: no route to host")
	client := NewClient(failingDoer{err: boom}, DefaultAPIBaseURL, "owner/repo", "govm/test")
	if _, err := client.LatestRelease(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("LatestRelease() error = %v, want wrapping %v", err, boom)
	}
}

func TestLatestReleaseHonoursCancelledContext(t *testing.T) {
	server := newServer(t, http.StatusOK, `{"tag_name":"v0.2.5"}`, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := NewClient(http.DefaultClient, server.URL, "owner/repo", "govm/test")
	if _, err := client.LatestRelease(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("LatestRelease() error = %v, want context.Canceled", err)
	}
}
