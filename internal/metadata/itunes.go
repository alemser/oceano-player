package metadata

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type itunesSongResult struct {
	ArtistName    string `json:"artistName"`
	TrackName     string `json:"trackName"`
	ArtworkUrl100 string `json:"artworkUrl100"`
}

// ItunesArtworkURL queries the iTunes Search API for the best-matching album
// artwork URL. Returns ("", nil) when nothing is found.
func ItunesArtworkURL(client *http.Client, artist, album string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" || album == "" {
		return "", nil
	}
	q := url.QueryEscape(artist + " " + album)
	apiURL := "https://itunes.apple.com/search?term=" + q + "&entity=album&limit=5&media=music"

	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("metadata itunes: album query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	var result struct {
		Results []struct {
			ArtistName     string `json:"artistName"`
			CollectionName string `json:"collectionName"`
			ArtworkUrl100  string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("metadata itunes: album decode: %w", err)
	}

	albumLower := strings.ToLower(album)
	artistLower := strings.ToLower(artist)

	for _, r := range result.Results {
		if strings.ToLower(r.CollectionName) == albumLower &&
			strings.Contains(strings.ToLower(r.ArtistName), artistLower) {
			return upgradeArtworkURL(r.ArtworkUrl100), nil
		}
	}
	if len(result.Results) > 0 && result.Results[0].ArtworkUrl100 != "" {
		return upgradeArtworkURL(result.Results[0].ArtworkUrl100), nil
	}
	return "", nil
}

// BestItunesSongArtworkURL picks the best artwork URL from iTunes song search
// results for the given artist and title (already trimmed).
func BestItunesSongArtworkURL(results []itunesSongResult, artist, title string) string {
	if len(results) == 0 || artist == "" || title == "" {
		return ""
	}
	titleLower := strings.ToLower(title)
	artistLower := strings.ToLower(artist)

	for _, r := range results {
		if strings.EqualFold(strings.TrimSpace(r.TrackName), title) &&
			strings.Contains(strings.ToLower(r.ArtistName), artistLower) &&
			r.ArtworkUrl100 != "" {
			return upgradeArtworkURL(r.ArtworkUrl100)
		}
	}
	for _, r := range results {
		if strings.Contains(strings.ToLower(r.TrackName), titleLower) &&
			strings.Contains(strings.ToLower(r.ArtistName), artistLower) &&
			r.ArtworkUrl100 != "" {
			return upgradeArtworkURL(r.ArtworkUrl100)
		}
	}
	if results[0].ArtworkUrl100 != "" {
		return upgradeArtworkURL(results[0].ArtworkUrl100)
	}
	return ""
}

// ItunesArtworkURLFromSong queries iTunes for a track and returns artwork
// from the best-matching song row.
func ItunesArtworkURLFromSong(client *http.Client, artist, title string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	if artist == "" || title == "" {
		return "", nil
	}
	q := url.QueryEscape(artist + " " + title)
	apiURL := "https://itunes.apple.com/search?term=" + q + "&entity=song&limit=8&media=music"

	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("metadata itunes: song query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	var envelope struct {
		Results []itunesSongResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("metadata itunes: song decode: %w", err)
	}

	return BestItunesSongArtworkURL(envelope.Results, artist, title), nil
}

// upgradeArtworkURL replaces the 100×100 iTunes thumbnail with a 600×600 version.
func upgradeArtworkURL(u string) string {
	return strings.Replace(u, "100x100bb", "600x600bb", 1)
}
