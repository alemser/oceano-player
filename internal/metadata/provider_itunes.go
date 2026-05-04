package metadata

import (
	"context"
	"net/http"
	"strings"
	"time"
)

const providerItunesID = "itunes"

// ItunesProvider resolves artwork via the iTunes Search API (no API key).
type ItunesProvider struct {
	HTTPClient *http.Client
	// ArtworkDir when non-empty causes successful lookups to download JPEGs
	// and populate Artwork.Path; otherwise only Artwork.URL is set.
	ArtworkDir string
}

func NewItunesProvider() *ItunesProvider {
	return &ItunesProvider{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *ItunesProvider) Name() string { return providerItunesID }

func (p *ItunesProvider) client() *http.Client {
	if p == nil || p.HTTPClient == nil {
		return http.DefaultClient
	}
	return p.HTTPClient
}

func (p *ItunesProvider) Enrich(ctx context.Context, req Request) (*Patch, error) {
	if p == nil || !req.WantArtwork {
		return &Patch{}, nil
	}
	artist := strings.TrimSpace(req.Artist)
	title := strings.TrimSpace(req.Title)
	album := strings.TrimSpace(req.Album)
	if artist == "" {
		return &Patch{}, nil
	}

	var imageURL string
	var err error
	if album != "" {
		imageURL, err = ItunesArtworkURL(p.client(), artist, album)
	} else if title != "" {
		imageURL, err = ItunesArtworkURLFromSong(p.client(), artist, title)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(imageURL) == "" {
		return &Patch{}, nil
	}

	out := &Patch{Provider: providerItunesID, Confidence: 80, Artwork: &ArtworkPatch{URL: imageURL}}
	dir := strings.TrimSpace(req.ArtworkDir)
	if dir == "" {
		dir = strings.TrimSpace(p.ArtworkDir)
	}
	if dir != "" {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, derr := SaveArtworkFromURL(p.client(), imageURL, dir)
		if derr != nil {
			return nil, derr
		}
		if path != "" {
			out.Artwork.Path = path
		}
	}
	return out, nil
}
