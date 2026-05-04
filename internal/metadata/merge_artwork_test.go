package metadata

import "testing"

func TestMergeArtworkOnlyPreservesProviderAndText(t *testing.T) {
	dst := &Patch{Provider: "discogs", Album: "A"}
	src := &Patch{Provider: "itunes", Artwork: &ArtworkPatch{URL: "http://u", Path: "/p.jpg"}}
	out := MergeArtworkOnly(dst, src)
	if out.Provider != "discogs" {
		t.Fatalf("provider = %q want discogs", out.Provider)
	}
	if out.Album != "A" || out.Artwork == nil || out.Artwork.Path != "/p.jpg" {
		t.Fatalf("unexpected merge: %+v", out)
	}
}

func TestMergeArtworkOnlyNilDst(t *testing.T) {
	src := &Patch{Provider: "itunes", Artwork: &ArtworkPatch{Path: "/x.jpg"}}
	out := MergeArtworkOnly(nil, src)
	if out.Artwork == nil || out.Artwork.Path != "/x.jpg" {
		t.Fatalf("got %+v", out)
	}
}
