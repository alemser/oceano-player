package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var artworkHTTPClient = &http.Client{Timeout: 10 * time.Second}

// saveArtworkFromURL downloads a JPEG from imageURL into dir using a
// content-addressed filename. Returns the file path on success, or ("", nil)
// when the HTTP response is not OK or the body is empty.
func saveArtworkFromURL(imageURL, dir string) (string, error) {
	resp, err := artworkHTTPClient.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("artwork: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("artwork: read body: %w", err)
	}

	sum := sha1.Sum(data)
	path := filepath.Join(dir, fmt.Sprintf("oceano-artwork-%x.jpg", sum[:4]))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("artwork: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("artwork: write: %w", err)
	}
	return path, nil
}

// fetchArtwork tries to find and download album artwork for artist+album,
// saving it as a content-addressed JPEG in dir. Returns the file path on
// success, or ("", nil) when no artwork is found.
//
// Provider order: iTunes Search API (fast, no credentials required).
func fetchArtwork(artist, album, dir string) (string, error) {
	imageURL, err := itunesArtworkURL(artist, album)
	if err != nil || imageURL == "" {
		return "", err
	}
	return saveArtworkFromURL(imageURL, dir)
}

// fetchArtworkFromSong resolves artwork via iTunes song search when album
// metadata is missing (common for Shazam-only matches). Returns ("", nil)
// when nothing matches.
func fetchArtworkFromSong(artist, title, dir string) (string, error) {
	imageURL, err := itunesArtworkURLFromSong(artist, title)
	if err != nil || imageURL == "" {
		return "", err
	}
	return saveArtworkFromURL(imageURL, dir)
}

// itunesArtworkURL queries the iTunes Search API for the best-matching album
// artwork URL. Returns ("", nil) when nothing is found.
func itunesArtworkURL(artist, album string) (string, error) {
	q := url.QueryEscape(artist + " " + album)
	apiURL := "https://itunes.apple.com/search?term=" + q + "&entity=album&limit=5&media=music"

	resp, err := artworkHTTPClient.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("artwork: itunes query: %w", err)
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
		return "", fmt.Errorf("artwork: itunes decode: %w", err)
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

type itunesSongResult struct {
	ArtistName    string `json:"artistName"`
	TrackName     string `json:"trackName"`
	ArtworkUrl100 string `json:"artworkUrl100"`
}

// bestItunesSongArtworkURL picks the best artwork URL from iTunes song search
// results for the given artist and title (already trimmed).
func bestItunesSongArtworkURL(results []itunesSongResult, artist, title string) string {
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

// itunesArtworkURLFromSong queries iTunes for a track and returns artwork
// from the best-matching song row.
func itunesArtworkURLFromSong(artist, title string) (string, error) {
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	if artist == "" || title == "" {
		return "", nil
	}
	q := url.QueryEscape(artist + " " + title)
	apiURL := "https://itunes.apple.com/search?term=" + q + "&entity=song&limit=8&media=music"

	resp, err := artworkHTTPClient.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("artwork: itunes song query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	var envelope struct {
		Results []itunesSongResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return "", fmt.Errorf("artwork: itunes song decode: %w", err)
	}

	return bestItunesSongArtworkURL(envelope.Results, artist, title), nil
}

// upgradeArtworkURL replaces the 100×100 iTunes thumbnail with a 600×600 version.
func upgradeArtworkURL(u string) string {
	return strings.Replace(u, "100x100bb", "600x600bb", 1)
}
