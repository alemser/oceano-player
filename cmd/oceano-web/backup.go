package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// generateBackup creates a compressed archive at destPath containing:
//   - library.db  (a clean copy of the SQLite database via VACUUM INTO)
//   - artwork/*   (all image files referenced by collection rows)
//   - restore.sh  (a script that copies both back to their original locations)
func (l *LibraryDB) generateBackup(destPath string) error {
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

	// 3. Create the .tar.gz archive in a temp file in the destination
	// directory, then rename it into place only after the write completes.
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

	// Add artwork files from the managed artwork directory only
	// (skip missing/unresolvable files and deduplicate by archive name).
	allowedArtworkDir, err := filepath.Abs(filepath.Join(filepath.Dir(l.path), "artwork"))
	if err != nil {
		return fmt.Errorf("backup: resolve artwork dir: %w", err)
	}
	if resolvedAllowedArtworkDir, err := filepath.EvalSymlinks(allowedArtworkDir); err == nil {
		allowedArtworkDir = resolvedAllowedArtworkDir
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

		relToAllowedDir, err := filepath.Rel(allowedArtworkDir, resolvedPath)
		if err != nil || relToAllowedDir == ".." || strings.HasPrefix(relToAllowedDir, ".."+string(os.PathSeparator)) {
			continue
		}

		// Use forward slash explicitly for OS-independent tar entry names.
		arcName := "artwork/" + filepath.Base(resolvedPath)
		if seenArtworks[arcName] {
			continue
		}
		seenArtworks[arcName] = true
		if err := addFile(resolvedPath, arcName); err != nil {
			return fmt.Errorf("backup: add artwork %s: %w", resolvedPath, err)
		}
	}

	// Add restore script.
	script := restoreScriptContent(l.path)
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

	cleanupTemp = false
	if err := f.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("backup: close temp archive: %w", err)
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("backup: rename temp archive: %w", err)
	}
	return nil
}

// restoreScriptContent returns a bash script that restores the database and
// artwork files from the extracted archive back to their original locations.
// Paths are single-quoted so the script is safe even if the configured path
// contains spaces or other shell-special characters.
func restoreScriptContent(dbPath string) string {
	artworkDir := filepath.Join(filepath.Dir(dbPath), "artwork")
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
# Oceano collection restore script.
# Extract the archive, then run: bash restore.sh
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
`, shellQuotePath(dbPath), shellQuotePath(artworkDir))
}

// shellQuotePath wraps a filesystem path in single quotes, escaping any
// embedded single quotes, so it can be safely embedded in a bash script.
func shellQuotePath(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// registerBackupRoute registers the GET /api/library/export/backup handler on
// mux. Each request generates a fresh archive containing the database snapshot,
// all referenced artwork images, and a bash restore script.
func registerBackupRoute(mux *http.ServeMux, libraryDBPath string) {
	mux.HandleFunc("/api/library/export/backup", func(w http.ResponseWriter, r *http.Request) {
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

		if err := lib.generateBackup(tmpPath); err != nil {
			http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		bf, err := os.Open(tmpPath)
		if err != nil {
			http.Error(w, "backup unavailable", http.StatusInternalServerError)
			return
		}
		defer bf.Close()
		fi, err := bf.Stat()
		if err != nil {
			http.Error(w, "backup unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="oceano-backup.tar.gz"`)
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		// Headers are already sent; errors here cannot change the HTTP status.
		io.Copy(w, bf) //nolint:errcheck
	})
}
