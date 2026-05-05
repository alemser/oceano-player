package metadata

import (
	"context"
	"strings"
)

// MergePolicy controls how provider patches are combined.
type MergePolicy string

const (
	MergePolicyFirstSuccess         MergePolicy = "first_success"
	MergePolicyFillMissingThenStop  MergePolicy = "fill_missing_then_stop"
	MergePolicyCollectAllBestEffort MergePolicy = "collect_all_best_effort"
)

// ProviderSpec mirrors one provider entry in metadata_enrichment.providers.
type ProviderSpec struct {
	ID      string
	Enabled bool
	Roles   []string
}

// ArtworkPatch carries optional artwork enrichment output.
type ArtworkPatch struct {
	URL  string
	Path string
}

// Patch contains additive enrichment data from one provider.
type Patch struct {
	Provider       string
	Confidence     int
	Album          string
	Label          string
	Released       string
	TrackNumber    string
	DiscogsURL     string
	Artwork        *ArtworkPatch
	// CandidatesJSON is a JSON-serialised []DiscogsEnrichment for the release
	// confirmation carousel. Populated only by the Discogs provider; empty for others.
	CandidatesJSON string
}

// Empty reports whether the patch carries no enrichment fields.
func (p *Patch) Empty() bool {
	if p == nil {
		return true
	}
	return strings.TrimSpace(p.Album) == "" &&
		strings.TrimSpace(p.Label) == "" &&
		strings.TrimSpace(p.Released) == "" &&
		strings.TrimSpace(p.TrackNumber) == "" &&
		strings.TrimSpace(p.DiscogsURL) == "" &&
		strings.TrimSpace(p.CandidatesJSON) == "" &&
		(p.Artwork == nil ||
			(strings.TrimSpace(p.Artwork.URL) == "" && strings.TrimSpace(p.Artwork.Path) == ""))
}

// Request is the provider-agnostic enrichment input.
type Request struct {
	Title       string
	Artist      string
	Album       string
	Label       string
	Released    string
	TrackNumber string
	DiscogsURL  string
	Format      string
	ACRID       string
	ShazamID    string
	ISRC        string
	// WantArtwork when true allows artwork providers (e.g. itunes) to run.
	WantArtwork bool
	// ArtworkDir is the directory for downloaded artwork (optional).
	ArtworkDir string
}

// Provider enriches metadata/artwork without writing persistence directly.
type Provider interface {
	Name() string
	Enrich(ctx context.Context, req Request) (*Patch, error)
}

