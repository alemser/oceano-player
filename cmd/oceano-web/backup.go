package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// generateBackup creates a compressed archive at destPath containing:
//   - library.db  (a clean copy of the SQLite database via VACUUM INTO)
//   - artwork/*   (image files referenced by collection rows, from the managed artwork directory only)
//   - restore.sh  (a script that copies both back to their original locations)
//
// The archive is written to a temporary file first and renamed into place
// only on success, so destPath is never left in a partial state.
func (l *LibraryDB) generateBackup(destPath, artworkDir string) error {
	// 1. Create a clean database copy using VACUUM INTO so the archive
	//    always contains a self-consistent snapshot.
	tmpDB, err := os.CreateTemp("", "oceano-db-backup-*.db")
	if err != nil {
		return fmt.Errorf("backup: temp db: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	tmpDB.Close()
	defer os.Remove(tmpDBPath)

	if _, err := l.db.Exec(`VACUUM INTO ?`, tmpDBPath); err != nil {
		return fmt.Errorf("backup: vacuum into: %w", err)
	}

	// 2. Collect distinct artwork paths referenced by the collection.
	rows, err := l.db.Query(`
		SELECT DISTINCT artwork_path FROM collection
		WHERE artwork_path IS NOT NULL AND artwork_path != ''`)
	if err != nil {
		return fmt.Errorf("backup: query artworks: %w", err)
	}
	var artworks []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return fmt.Errorf("backup: scan artwork: %w", err)
		}
		artworks = append(artworks, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("backup: artworks: %w", err)
	}

	// 3. Write the archive to a temp file in the destination directory,
	//    then rename it atomically so destPath is never partially written.
	destDir := filepath.Dir(destPath)
	tempFile, err := os.CreateTemp(destDir, filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("backup: create temp archive: %w", err)
	}
	tempPath := tempFile.Name()
	f := tempFile

	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = f.Close()
			_ = os.Remove(tempPath)
		}
	}()

	gw, err := gzip.NewWriterLevel(f, gzip.DefaultCompression)
	if err != nil {
		return fmt.Errorf("backup: gzip writer: %w", err)
	}
	tw := tar.NewWriter(gw)

	addFile := func(srcPath, arcName string) error {
		fi, err := os.Stat(srcPath)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name:    arcName,
			Size:    fi.Size(),
			Mode:    int64(fi.Mode().Perm()),
			ModTime: fi.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	}

	// Add database snapshot.
	if err := addFile(tmpDBPath, "library.db"); err != nil {
		return fmt.Errorf("backup: add db: %w", err)
	}

	// Add artwork files from the managed artwork directory only.
	// Reject symlinks and any path that resolves outside the expected artwork dir
	// to prevent exfiltration of arbitrary files via crafted artwork_path values.
	allowedArtworkDir, err := filepath.Abs(artworkDir)
	if err != nil {
		return fmt.Errorf("backup: resolve artwork dir: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(allowedArtworkDir); err == nil {
		allowedArtworkDir = resolved
	}

	seenArtworks := make(map[string]bool)
	for _, ap := range artworks {
		if ap == "" {
			continue
		}

		info, err := os.Lstat(ap)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}

		resolvedPath, err := filepath.EvalSymlinks(ap)
		if err != nil {
			continue
		}

		// Only include files that live inside the managed artwork directory.
		rel, err := filepath.Rel(allowedArtworkDir, resolvedPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}

		// Preserve the path relative to the managed artwork directory.
		// Use forward slashes explicitly — tar paths must be POSIX-style.
		arcName := "artwork/" + filepath.ToSlash(rel)
		if seenArtworks[arcName] {
			continue
		}
		seenArtworks[arcName] = true
		if err := addFile(resolvedPath, arcName); err != nil {
			return fmt.Errorf("backup: add artwork %s: %w", resolvedPath, err)
		}
	}

	// Add restore script.
	script := restoreScriptContent(l.path, artworkDir)
	hdr := &tar.Header{
		Name:    "restore.sh",
		Size:    int64(len(script)),
		Mode:    0o755,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("backup: restore script header: %w", err)
	}
	if _, err := io.WriteString(tw, script); err != nil {
		return fmt.Errorf("backup: restore script body: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: tar close: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("backup: gzip close: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("backup: close temp file: %w", err)
	}
	cleanupTemp = false

	if err := os.Rename(tempPath, destPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("backup: rename: %w", err)
	}
	return nil
}

// shellQuote returns s wrapped in single quotes, safe for use in bash scripts.
// Any single quotes inside s are escaped as '\”.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// restoreScriptContent returns a bash script that restores the database and
// artwork files from the extracted archive back to their original locations.
// Paths are single-quoted to prevent shell injection.
func restoreScriptContent(dbPath, artworkDir string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
# Oceano collection restore script.
# Extract the archive first, then run: bash restore.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DB_DEST=%s
ARTWORK_DEST=%s

mkdir -p "$(dirname "$DB_DEST")"
cp "$SCRIPT_DIR/library.db" "$DB_DEST"
echo "Database restored to $DB_DEST"

if [ -d "$SCRIPT_DIR/artwork" ]; then
  mkdir -p "$ARTWORK_DEST"
  cp -r "$SCRIPT_DIR/artwork/." "$ARTWORK_DEST/"
  echo "Artwork restored to $ARTWORK_DEST"
fi

echo "Restore complete."
`, shellQuote(dbPath), shellQuote(artworkDir))
}

// registerBackupRoute wires the backup download endpoint into mux.
// Registered at both the canonical path (/api/libraries/physical/export/backup)
// and the legacy alias (/api/library/export/backup).
// Each GET request generates a fresh archive and streams it as a download.
func registerBackupRoute(mux *http.ServeMux, libraryDBPath, artworkDir string) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if lib == nil {
			http.Error(w, "library not initialised", http.StatusServiceUnavailable)
			return
		}
		defer lib.close()

		tmp, err := os.CreateTemp("", "oceano-backup-*.tar.gz")
		if err != nil {
			http.Error(w, "cannot create backup", http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		tmp.Close()
		defer os.Remove(tmpPath)

		if err := lib.generateBackup(tmpPath, artworkDir); err != nil {
			http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		bf, err := os.Open(tmpPath)
		if err != nil {
			http.Error(w, "backup unavailable", http.StatusInternalServerError)
			return
		}
		defer bf.Close()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="oceano-backup.tar.gz"`)
		if fi, err := bf.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		}
		// Headers already sent; log any copy error rather than returning it.
		if _, err := io.Copy(w, bf); err != nil {
			log.Printf("backup: stream to client: %v", err)
		}
	}
	mux.HandleFunc("/api/libraries/physical/export/backup", handler)
	mux.HandleFunc("/api/library/export/backup", handler)
}
