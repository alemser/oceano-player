package metadata

import (
	"context"
	"strings"
)

const providerPayloadID = "provider_payload"

// PayloadProvider exposes non-empty recognition fields as a baseline enrichment patch.
type PayloadProvider struct{}

func NewPayloadProvider() *PayloadProvider { return &PayloadProvider{} }

func (PayloadProvider) Name() string { return providerPayloadID }

func (PayloadProvider) Enrich(_ context.Context, req Request) (*Patch, error) {
	p := &Patch{Provider: providerPayloadID, Confidence: 100}
	if s := strings.TrimSpace(req.Album); s != "" {
		p.Album = s
	}
	if s := strings.TrimSpace(req.Label); s != "" {
		p.Label = s
	}
	if s := strings.TrimSpace(req.Released); s != "" {
		p.Released = s
	}
	if s := strings.TrimSpace(req.TrackNumber); s != "" {
		p.TrackNumber = s
	}
	if s := strings.TrimSpace(req.DiscogsURL); s != "" {
		p.DiscogsURL = s
	}
	if p.Empty() {
		return &Patch{}, nil
	}
	return p, nil
}
