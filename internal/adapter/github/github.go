// Package github implements upgrade.LatestReleaseSource on top of the
// GitHub REST API. It is the only place govm knows about GitHub.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SmileOniks/govm/internal/utils"
)

// DefaultAPIBaseURL is the public GitHub REST API root.
const DefaultAPIBaseURL = "https://api.github.com"

// maxResponseBytes bounds how much of a releases payload is read. The
// latest-release object is a few kilobytes; anything larger is not a
// response worth trusting.
const maxResponseBytes = 1 << 20

// Client fetches the Latest release of one repository. It satisfies
// upgrade.LatestReleaseSource.
type Client struct {
	httpClient utils.Doer
	url        string
	userAgent  string
}

// NewClient builds a Client for "owner/name" against baseURL. userAgent
// is sent with every request: GitHub rejects requests without one.
func NewClient(httpClient utils.Doer, baseURL, repository, userAgent string) *Client {
	return &Client{
		httpClient: httpClient,
		url:        strings.TrimRight(baseURL, "/") + "/repos/" + strings.Trim(repository, "/") + "/releases/latest",
		userAgent:  userAgent,
	}
}

type latestReleasePayload struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// LatestRelease returns the tag of the newest stable release. The
// endpoint already excludes drafts and pre-releases; the flags are
// still checked so a surprising payload never yields a notice.
func (c *Client) LatestRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("fetch latest release: unexpected status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	var payload latestReleasePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	if payload.Draft || payload.Prerelease {
		return "", errors.New("fetch latest release: payload is not a stable release")
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", errors.New("fetch latest release: empty tag_name")
	}
	return tag, nil
}
