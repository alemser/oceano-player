package recognition

import "testing"

func TestResultFromACRMusic_RejectsAlbumNameAsTitle(t *testing.T) {
	got := resultFromACRMusic(acrMusic{
		ACRID:  "d12ea8f70e2d5899330f19bdb28c3957",
		Title:  "Communiqué",
		Artists: []acrArtist{{Name: "Dire Straits"}},
		Album:   acrAlbum{Name: "Communiqué"},
		Score:   25,
	})
	if got != nil {
		t.Fatalf("expected nil when title equals album, got %+v", got)
	}
}

func TestResultFromACRMusic_AcceptsDistinctTrackTitle(t *testing.T) {
	got := resultFromACRMusic(acrMusic{
		ACRID:   "x",
		Title:   "Once Upon a Time in the West",
		Artists: []acrArtist{{Name: "Dire Straits"}},
		Album:   acrAlbum{Name: "Communiqué"},
		Score:   90,
	})
	if got == nil || got.Title != "Once Upon a Time in the West" || got.Album != "Communiqué" {
		t.Fatalf("unexpected result: %+v", got)
	}
}
