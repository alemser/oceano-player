package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const backupMaxHistory = 7

// BackupInfo describes a stored backup file.
type BackupInfo struct {
	File      string    `json:"file"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

// backupFileName returns a UTC-timestamped backup filename.
func backupFileName() string {
	return "oceano-backup-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
}

// listBackups returns available backup files in dir, sorted newest first.
func listBackups(backupDir string) ([]BackupInfo, error) {
	entries, err := filepath.Glob(filepath.Join(backupDir, "oceano-backup-*.tar.gz"))
	if err != nil {
		return nil, err
	}
	var infos []BackupInfo
	for _, p := range entries {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		infos = append(infos, BackupInfo{
			File:      filepath.Base(p),
			CreatedAt: fi.ModTime().UTC(),
			SizeBytes: fi.Size(),
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CreatedAt.After(infos[j].CreatedAt)
	})
	return infos, nil
}

// pruneOldBackups deletes the oldest backup files, keeping at most `keep`.
func pruneOldBackups(backupDir string, keep int) {
	infos, err := listBackups(backupDir)
	if err != nil || len(infos) <= keep {
		return
	}
	for _, b := range infos[keep:] {
		path := filepath.Join(backupDir, b.File)
		if err := os.Remove(path); err == nil {
			log.Printf("backup: pruned %s", b.File)
		}
	}
}

// generateBackup creates a compressed archive at destPath containing:
//   - library.db  (a clean copy of the SQLite database via VACUUM INTO)
//   - artwork/*   (image files referenced by collection rows, from the managed artwork directory only)
//   - config.json (the Oceano config file, if it exists at configPath)
//   - restore.sh  (a script that copies all files back to their original locations)
//
// The archive is written to a temporary file first and renamed into place
// only on success, so destPath is never left in a partial state.
func (l *LibraryDB) generateBackup(destPath, artworkDir, configPath string) error {
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

	// Add config file if present (optional — backup is still valid without it).
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			if err := addFile(configPath, "config.json"); err != nil {
				return fmt.Errorf("backup: add config: %w", err)
			}
		}
	}

	// Add restore script.
	script := restoreScriptContent(l.path, artworkDir, configPath)
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

// restoreScriptContent returns a bash script that restores the database,
// artwork files, and optionally the config from the extracted archive.
// Paths are single-quoted to prevent shell injection.
func restoreScriptContent(dbPath, artworkDir, configPath string) string {
	configBlock := ""
	if configPath != "" {
		configBlock = fmt.Sprintf(`
if [ -f "$SCRIPT_DIR/config.json" ]; then
  mkdir -p "$(dirname %s)"
  cp "$SCRIPT_DIR/config.json" %s
  echo "Config restored to %s"
fi
`, shellQuote(configPath), shellQuote(configPath), configPath)
	}
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
%s
echo "Restore complete."
`, shellQuote(dbPath), shellQuote(artworkDir), configBlock)
}

// restoreFileToPath writes the content from r atomically to destPath,
// using a temp file + rename to avoid leaving a partial file on error.
func restoreFileToPath(r io.Reader, mode int64, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".restore-*")
	if err != nil {
		return fmt.Errorf("temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("copy: %w", err)
	}
	perm := os.FileMode(mode & 0o777)
	if perm == 0 {
		perm = 0o644
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	cleanup = false
	return os.Rename(tmpPath, destPath)
}

// restoreFromBackup extracts the requested scope from the backup archive at
// backupPath. scope must be "library", "config", or "both".
// Returns a list of human-readable result messages.
func restoreFromBackup(backupPath, scope, libraryDBPath, artworkDir, configPath string) ([]string, error) {
	f, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	restoreLib := scope == "library" || scope == "both"
	restoreCfg := scope == "config" || scope == "both"

	artworkRestored := 0
	var msgs []string

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return msgs, fmt.Errorf("read archive: %w", err)
		}

		switch {
		case hdr.Name == "library.db" && restoreLib:
			if err := restoreFileToPath(tr, hdr.Mode, libraryDBPath); err != nil {
				return msgs, fmt.Errorf("restore library.db: %w", err)
			}
			msgs = append(msgs, "Library database restored")

		case strings.HasPrefix(hdr.Name, "artwork/") && restoreLib:
			rel := strings.TrimPrefix(hdr.Name, "artwork/")
			if rel == "" || strings.Contains(rel, "..") {
				continue
			}
			destPath := filepath.Join(artworkDir, filepath.FromSlash(rel))
			if hdr.Typeflag == tar.TypeDir {
				_ = os.MkdirAll(destPath, 0o755)
				continue
			}
			if err := restoreFileToPath(tr, hdr.Mode, destPath); err != nil {
				log.Printf("restore: artwork %s: %v", rel, err)
				continue
			}
			artworkRestored++

		case hdr.Name == "config.json" && restoreCfg:
			if err := restoreFileToPath(tr, hdr.Mode, configPath); err != nil {
				return msgs, fmt.Errorf("restore config.json: %w", err)
			}
			msgs = append(msgs, "Configuration restored")
		}
	}

	if restoreLib && artworkRestored > 0 {
		msgs = append(msgs, fmt.Sprintf("%d artwork file(s) restored", artworkRestored))
	}
	return msgs, nil
}

// registerBackupRoutes wires all backup and restore API endpoints into mux.
//
//	GET  /api/backups              — list available backups (JSON array)
//	GET  /api/backups/download     — download a specific backup (?file=<name>)
//	POST /api/backups/run          — trigger an immediate backup
//	POST /api/backups/restore      — restore from a backup (body: {file, scope})
//	GET  /api/library/export/backup — legacy redirect to latest backup download
func registerBackupRoutes(mux *http.ServeMux, libraryDBPath, artworkDir, configPath string) {
	backupDir := filepath.Dir(libraryDBPath)

	// GET /api/backups — list available backups.
	mux.HandleFunc("/api/backups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		infos, err := listBackups(backupDir)
		if err != nil {
			http.Error(w, "list backups: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if infos == nil {
			infos = []BackupInfo{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(infos)
	})

	// GET /api/backups/download?file=<name> — download a specific backup.
	mux.HandleFunc("/api/backups/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := r.URL.Query().Get("file")
		if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
			http.Error(w, "invalid file parameter", http.StatusBadRequest)
			return
		}
		path := filepath.Join(backupDir, name)
		bf, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer bf.Close()
		fi, err := bf.Stat()
		if err != nil {
			http.Error(w, "stat failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		if _, err := io.Copy(w, bf); err != nil {
			log.Printf("backup download: stream: %v", err)
		}
	})

	// POST /api/backups/run — trigger an immediate backup now.
	mux.HandleFunc("/api/backups/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil || lib == nil {
			http.Error(w, "library not available", http.StatusServiceUnavailable)
			return
		}
		defer lib.close()

		destPath := filepath.Join(backupDir, backupFileName())
		if err := lib.generateBackup(destPath, artworkDir, configPath); err != nil {
			http.Error(w, "backup failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		pruneOldBackups(backupDir, backupMaxHistory)
		log.Printf("on-demand backup written to %s", destPath)

		infos, _ := listBackups(backupDir)
		if infos == nil {
			infos = []BackupInfo{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "backups": infos})
	})

	// POST /api/backups/restore — restore from a specific backup.
	// Body: {"file":"oceano-backup-….tar.gz","scope":"library"|"config"|"both"}
	mux.HandleFunc("/api/backups/restore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			File  string `json:"file"`
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.File == "" || strings.ContainsAny(req.File, "/\\") || strings.Contains(req.File, "..") {
			http.Error(w, "invalid file", http.StatusBadRequest)
			return
		}
		if req.Scope != "library" && req.Scope != "config" && req.Scope != "both" {
			http.Error(w, `scope must be "library", "config", or "both"`, http.StatusBadRequest)
			return
		}

		backupPath := filepath.Join(backupDir, req.File)
		if _, err := os.Stat(backupPath); err != nil {
			http.NotFound(w, r)
			return
		}

		msgs, err := restoreFromBackup(backupPath, req.Scope, libraryDBPath, artworkDir, configPath)
		if err != nil {
			http.Error(w, "restore failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// If the config was restored, restart affected services.
		if req.Scope == "config" || req.Scope == "both" {
			newCfg, cfgErr := loadConfig(configPath)
			if cfgErr == nil {
				if _, err := os.Stat(detectorSvc); err == nil {
					if wErr := writeDetectorService(newCfg); wErr == nil {
						if rErr := restartService(detectorUnit); rErr == nil {
							msgs = append(msgs, "oceano-source-detector restarted")
						} else {
							msgs = append(msgs, "detector restart: "+rErr.Error())
						}
					}
				}
				if _, err := os.Stat(managerSvc); err == nil {
					if wErr := writeManagerService(newCfg, configPath); wErr == nil {
						if rErr := restartService(managerUnit); rErr == nil {
							msgs = append(msgs, "oceano-state-manager restarted")
						} else {
							msgs = append(msgs, "manager restart: "+rErr.Error())
						}
					}
				}
			}
		}

		log.Printf("restore from %s (scope=%s): %v", req.File, req.Scope, msgs)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "results": msgs})
	})

	// POST /api/backups/upload-restore — accept an uploaded backup archive and restore from it.
	// Multipart form fields: "backup" (file), "scope" ("library"|"config"|"both").
	// The uploaded file is held in a temp file and never added to the backup history.
	mux.HandleFunc("/api/backups/upload-restore", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Limit upload to 512 MB to prevent accidental huge uploads.
		if err := r.ParseMultipartForm(512 << 20); err != nil {
			http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
			return
		}
		scope := r.FormValue("scope")
		if scope != "library" && scope != "config" && scope != "both" {
			http.Error(w, `scope must be "library", "config", or "both"`, http.StatusBadRequest)
			return
		}
		f, hdr, err := r.FormFile("backup")
		if err != nil {
			http.Error(w, "backup file missing: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer f.Close()
		_ = hdr

		// Write upload to a temp file so restoreFromBackup can seek through it.
		tmp, err := os.CreateTemp("", "oceano-upload-restore-*.tar.gz")
		if err != nil {
			http.Error(w, "temp file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)

		if _, err := io.Copy(tmp, f); err != nil {
			tmp.Close()
			http.Error(w, "write upload: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmp.Close()

		msgs, err := restoreFromBackup(tmpPath, scope, libraryDBPath, artworkDir, configPath)
		if err != nil {
			http.Error(w, "restore failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Restart affected services if config was restored.
		if scope == "config" || scope == "both" {
			newCfg, cfgErr := loadConfig(configPath)
			if cfgErr == nil {
				if _, err := os.Stat(detectorSvc); err == nil {
					if wErr := writeDetectorService(newCfg); wErr == nil {
						if rErr := restartService(detectorUnit); rErr == nil {
							msgs = append(msgs, "oceano-source-detector restarted")
						} else {
							msgs = append(msgs, "detector restart: "+rErr.Error())
						}
					}
				}
				if _, err := os.Stat(managerSvc); err == nil {
					if wErr := writeManagerService(newCfg, configPath); wErr == nil {
						if rErr := restartService(managerUnit); rErr == nil {
							msgs = append(msgs, "oceano-state-manager restarted")
						} else {
							msgs = append(msgs, "manager restart: "+rErr.Error())
						}
					}
				}
			}
		}

		log.Printf("upload-restore (scope=%s): %v", scope, msgs)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "results": msgs})
	})

	// GET /api/library/export/backup — legacy endpoint: redirect to latest backup.
	mux.HandleFunc("/api/library/export/backup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		infos, err := listBackups(backupDir)
		if err == nil && len(infos) > 0 {
			http.Redirect(w, r, "/api/backups/download?file="+infos[0].File, http.StatusFound)
			return
		}
		// No history yet — generate on-demand for the first time.
		lib, err := openLibraryDB(libraryDBPath)
		if err != nil || lib == nil {
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

		if err := lib.generateBackup(tmpPath, artworkDir, configPath); err != nil {
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
		if _, err := io.Copy(w, bf); err != nil {
			log.Printf("backup: stream to client: %v", err)
		}
	})
}
