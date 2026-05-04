package metadata

import (
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// SaveArtworkFromURL downloads a JPEG from imageURL into dir using a
// content-addressed filename. Returns the file path on success, or ("", nil)
// when the HTTP response is not OK or the body is empty.
func SaveArtworkFromURL(client *http.Client, imageURL, dir string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("metadata artwork: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("metadata artwork: read body: %w", err)
	}

	sum := sha1.Sum(data)
	path := filepath.Join(dir, fmt.Sprintf("oceano-artwork-%x.jpg", sum[:4]))
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("metadata artwork: mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("metadata artwork: write: %w", err)
	}
	return path, nil
}
