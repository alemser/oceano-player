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

	// Download image.
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

	// Content-addressed filename — reuses existing file if already downloaded.
	sum := sha1.Sum(data)
	path := filepath.Join(dir, fmt.Sprintf("oceano-artwork-%x.jpg", sum[:4]))
	if _, err := os.Stat(path); err == nil {
		return path, nil // already on disk
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("artwork: write: %w", err)
	}
	return path, nil
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
			ArtistName      string `json:"artistName"`
			CollectionName  string `json:"collectionName"`
			ArtworkUrl100   string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("artwork: itunes decode: %w", err)
	}

	albumLower  := strings.ToLower(album)
	artistLower := strings.ToLower(artist)

	// Prefer an exact album+artist match; fall back to first result.
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

// upgradeArtworkURL replaces the 100×100 iTunes thumbnail with a 600×600 version.
func upgradeArtworkURL(u string) string {
	return strings.Replace(u, "100x100bb", "600x600bb", 1)
}
