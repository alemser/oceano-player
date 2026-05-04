package metadata

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/alemser/oceano-player/internal/recognition"
)

const providerDiscogsID = "discogs"

// DiscogsProvider adapts recognition.DiscogsClient as a metadata enrichment provider.
type DiscogsProvider struct {
	client *recognition.DiscogsClient
}

// NewDiscogsProvider returns a provider wrapping client, or nil when client is nil.
func NewDiscogsProvider(client *recognition.DiscogsClient) *DiscogsProvider {
	if client == nil {
		return nil
	}
	return &DiscogsProvider{client: client}
}

func (p *DiscogsProvider) Name() string { return providerDiscogsID }

func (p *DiscogsProvider) Enrich(ctx context.Context, req Request) (*Patch, error) {
	if p == nil || p.client == nil {
		return &Patch{}, nil
	}
	artist := strings.TrimSpace(req.Artist)
	title := strings.TrimSpace(req.Title)
	if artist == "" || title == "" {
		return &Patch{}, nil
	}

	enriched, err := p.client.EnrichTrack(ctx, artist, title, strings.TrimSpace(req.Album), strings.TrimSpace(req.Format))
	if err != nil {
		if errors.Is(err, recognition.ErrRateLimit) {
			log.Printf("discogs provider: rate limited")
			return &Patch{}, nil
		}
		return nil, err
	}
	if enriched == nil {
		return &Patch{}, nil
	}

	out := &Patch{
		Provider:   providerDiscogsID,
		Confidence: enriched.Score,
		Album:      strings.TrimSpace(enriched.Album),
		Label:      strings.TrimSpace(enriched.Label),
		Released:   strings.TrimSpace(enriched.Released),
		DiscogsURL: strings.TrimSpace(enriched.DiscogsURL),
	}
	if req.WantArtwork {
		imageURL := strings.TrimSpace(enriched.CoverImage)
		if imageURL != "" {
			art := &ArtworkPatch{URL: imageURL}
			if dir := strings.TrimSpace(req.ArtworkDir); dir != "" {
				path, derr := SaveArtworkFromURL(nil, imageURL, dir)
				if derr != nil {
					return nil, derr
				}
				art.Path = path
			}
			out.Artwork = art
		}
	}
	return out, nil
}
