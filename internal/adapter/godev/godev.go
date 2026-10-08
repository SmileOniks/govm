package godev

import (
	"context"
	"net/http"
	"strings"

	"github.com/SmileOniks/govm/internal/loader"
	"github.com/SmileOniks/govm/internal/utils"
)

// Client implements loader.ReleaseSource by fetching the go.dev
// release catalog and mapping it to the loader domain model.
type Client struct {
	httpClient utils.Doer
	url        string
}

// NewClient constructs a Client that fetches releases from the given
// URL using the provided HTTP client. The client's timeout is
// controlled by the caller.
func NewClient(httpClient utils.Doer, url string) *Client {
	return &Client{
		httpClient: httpClient,
		url:        url,
	}
}

func NewClientForSource(httpClient utils.Doer, source string) *Client {
	return NewClient(httpClient, strings.TrimRight(source, "/")+"/?mode=json&include=all")
}

// FetchReleases fetches the go.dev release catalog and maps it to
// loader.Release entries. The Version field is normalized (no "go"
// prefix) by utils.FetchGoDevReleasesWithRequest.
func (c *Client) FetchReleases(ctx context.Context) ([]loader.Release, error) {
	req, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	godevReleases, err := utils.FetchGoDevReleasesWithRequest(c.httpClient, req)
	if err != nil {
		return nil, err
	}

	releases := make([]loader.Release, 0, len(godevReleases))
	for _, gdr := range godevReleases {
		files := make([]loader.FileEntry, 0, len(gdr.Files))
		for _, gdf := range gdr.Files {
			files = append(files, loader.FileEntry{
				Filename: gdf.Filename,
				OS:       gdf.OS,
				Arch:     gdf.Arch,
				Kind:     gdf.Kind,
				SHA256:   gdf.SHA256,
				Size:     gdf.Size,
			})
		}
		releases = append(releases, loader.Release{
			Version: gdr.Version,
			Stable:  gdr.Stable,
			Files:   files,
		})
	}

	return releases, nil
}
