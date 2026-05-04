package main

import (
	"net/http"
	"time"

	"github.com/alemser/oceano-player/internal/metadata"
)

var artworkHTTPClient = &http.Client{Timeout: 10 * time.Second}

// saveArtworkFromURL downloads a JPEG from imageURL into dir using a
// content-addressed filename. Returns the file path on success, or ("", nil)
// when the HTTP response is not OK or the body is empty.
func saveArtworkFromURL(imageURL, dir string) (string, error) {
	return metadata.SaveArtworkFromURL(artworkHTTPClient, imageURL, dir)
}

// fetchArtwork tries to find and download album artwork for artist+album,
// saving it as a content-addressed JPEG in dir. Returns the file path on
// success, or ("", nil) when no artwork is found.
//
// Provider order: iTunes Search API (fast, no credentials required).
func fetchArtwork(artist, album, dir string) (string, error) {
	imageURL, err := metadata.ItunesArtworkURL(artworkHTTPClient, artist, album)
	if err != nil || imageURL == "" {
		return "", err
	}
	return saveArtworkFromURL(imageURL, dir)
}

// fetchArtworkFromSong resolves artwork via iTunes song search when album
// metadata is missing (common for Shazamio-only matches). Returns ("", nil)
// when nothing matches.
func fetchArtworkFromSong(artist, title, dir string) (string, error) {
	imageURL, err := metadata.ItunesArtworkURLFromSong(artworkHTTPClient, artist, title)
	if err != nil || imageURL == "" {
		return "", err
	}
	return saveArtworkFromURL(imageURL, dir)
}
