package metadata

import "strings"

// missingMask records which enrichment slots were empty in the seed patch
// before the chain run. Used by MergePolicyFillMissingThenStop for early exit.
type missingMask struct {
	album       bool
	label       bool
	released    bool
	trackNumber bool
	discogsURL  bool
	artwork     bool
}

func newMissingMask(seed *Patch) missingMask {
	if seed == nil {
		return missingMask{
			album: true, label: true, released: true, trackNumber: true,
			discogsURL: true, artwork: true,
		}
	}
	m := missingMask{}
	if strings.TrimSpace(seed.Album) == "" {
		m.album = true
	}
	if strings.TrimSpace(seed.Label) == "" {
		m.label = true
	}
	if strings.TrimSpace(seed.Released) == "" {
		m.released = true
	}
	if strings.TrimSpace(seed.TrackNumber) == "" {
		m.trackNumber = true
	}
	if strings.TrimSpace(seed.DiscogsURL) == "" {
		m.discogsURL = true
	}
	if seed.Artwork == nil || artworkSlotEmpty(seed.Artwork) {
		m.artwork = true
	}
	return m
}

func artworkSlotEmpty(a *ArtworkPatch) bool {
	if a == nil {
		return true
	}
	return strings.TrimSpace(a.URL) == "" && strings.TrimSpace(a.Path) == ""
}

func (m missingMask) satisfiedBy(out *Patch) bool {
	if out == nil {
		return false
	}
	if m.album && strings.TrimSpace(out.Album) == "" {
		return false
	}
	if m.label && strings.TrimSpace(out.Label) == "" {
		return false
	}
	if m.released && strings.TrimSpace(out.Released) == "" {
		return false
	}
	if m.trackNumber && strings.TrimSpace(out.TrackNumber) == "" {
		return false
	}
	if m.discogsURL && strings.TrimSpace(out.DiscogsURL) == "" {
		return false
	}
	if m.artwork && (out.Artwork == nil || artworkSlotEmpty(out.Artwork)) {
		return false
	}
	return true
}

func (m missingMask) any() bool {
	return m.album || m.label || m.released || m.trackNumber || m.discogsURL || m.artwork
}

// ClonePatch returns a deep copy of p, or an empty non-nil patch when p is nil.
func ClonePatch(p *Patch) *Patch {
	out := &Patch{}
	if p == nil {
		return out
	}
	out.Provider = p.Provider
	out.Confidence = p.Confidence
	out.Album = p.Album
	out.Label = p.Label
	out.Released = p.Released
	out.TrackNumber = p.TrackNumber
	out.DiscogsURL = p.DiscogsURL
	if p.Artwork != nil {
		out.Artwork = &ArtworkPatch{URL: p.Artwork.URL, Path: p.Artwork.Path}
	}
	return out
}

// mergeFillMissing copies non-empty fields from src into dst when dst fields are empty.
// Returns true if dst changed.
func mergeFillMissing(dst *Patch, src *Patch) bool {
	if dst == nil || src == nil || src.Empty() {
		return false
	}
	changed := false
	if strings.TrimSpace(dst.Album) == "" && strings.TrimSpace(src.Album) != "" {
		dst.Album = strings.TrimSpace(src.Album)
		changed = true
	}
	if strings.TrimSpace(dst.Label) == "" && strings.TrimSpace(src.Label) != "" {
		dst.Label = strings.TrimSpace(src.Label)
		changed = true
	}
	if strings.TrimSpace(dst.Released) == "" && strings.TrimSpace(src.Released) != "" {
		dst.Released = strings.TrimSpace(src.Released)
		changed = true
	}
	if strings.TrimSpace(dst.TrackNumber) == "" && strings.TrimSpace(src.TrackNumber) != "" {
		dst.TrackNumber = strings.TrimSpace(src.TrackNumber)
		changed = true
	}
	if strings.TrimSpace(dst.DiscogsURL) == "" && strings.TrimSpace(src.DiscogsURL) != "" {
		dst.DiscogsURL = strings.TrimSpace(src.DiscogsURL)
		changed = true
	}
	if (dst.Artwork == nil || artworkSlotEmpty(dst.Artwork)) && src.Artwork != nil && !artworkSlotEmpty(src.Artwork) {
		dst.Artwork = &ArtworkPatch{URL: src.Artwork.URL, Path: src.Artwork.Path}
		changed = true
	}
	if src.Confidence > dst.Confidence {
		dst.Confidence = src.Confidence
	}
	if changed && strings.TrimSpace(dst.Provider) == "" {
		dst.Provider = src.Provider
	} else if changed && strings.TrimSpace(src.Provider) != "" {
		dst.Provider = src.Provider
	}
	return changed
}

// mergeOverwriteNonEmpty sets dst fields from src where src has non-empty values.
func mergeOverwriteNonEmpty(dst *Patch, src *Patch) {
	if dst == nil || src == nil {
		return
	}
	if strings.TrimSpace(src.Album) != "" {
		dst.Album = strings.TrimSpace(src.Album)
	}
	if strings.TrimSpace(src.Label) != "" {
		dst.Label = strings.TrimSpace(src.Label)
	}
	if strings.TrimSpace(src.Released) != "" {
		dst.Released = strings.TrimSpace(src.Released)
	}
	if strings.TrimSpace(src.TrackNumber) != "" {
		dst.TrackNumber = strings.TrimSpace(src.TrackNumber)
	}
	if strings.TrimSpace(src.DiscogsURL) != "" {
		dst.DiscogsURL = strings.TrimSpace(src.DiscogsURL)
	}
	if src.Artwork != nil && !artworkSlotEmpty(src.Artwork) {
		dst.Artwork = &ArtworkPatch{URL: src.Artwork.URL, Path: src.Artwork.Path}
	}
	if src.Confidence > dst.Confidence {
		dst.Confidence = src.Confidence
	}
	if strings.TrimSpace(src.Provider) != "" {
		dst.Provider = src.Provider
	}
}
