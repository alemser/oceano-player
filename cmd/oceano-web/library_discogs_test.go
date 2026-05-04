package main

import (
	"testing"
	"time"
)

func TestLibraryList_IncludesDiscogsURL(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := lib.db.Exec(`
		INSERT INTO collection (acrid, title, artist, play_count, first_played, last_played, discogs_url)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		"acr-web-discogs-1", "Track", "Artist", now, now, "https://api.discogs.com/releases/777"); err != nil {
		t.Fatalf("insert row with discogs_url: %v", err)
	}

	rows, err := lib.list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	if rows[0].DiscogsURL != "https://api.discogs.com/releases/777" {
		t.Fatalf("discogs_url=%q want persisted URL", rows[0].DiscogsURL)
	}
}

func TestLibraryChangesSince_IncludesDiscogsURLInUpserts(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, nil)
	lib, err := openLibraryDB(dbPath)
	if err != nil || lib == nil {
		t.Fatalf("openLibraryDB: err=%v lib=%v", err, lib)
	}
	defer lib.close()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := lib.db.Exec(`
		INSERT INTO collection (acrid, title, artist, play_count, first_played, last_played)
		VALUES (?, ?, ?, 1, ?, ?)`,
		"acr-web-discogs-2", "Track", "Artist", now, now); err != nil {
		t.Fatalf("insert row: %v", err)
	}

	v0, err := lib.getLibraryVersion()
	if err != nil {
		t.Fatalf("getLibraryVersion: %v", err)
	}
	if _, err := lib.db.Exec(`UPDATE collection SET discogs_url=? WHERE acrid=?`, "https://api.discogs.com/releases/888", "acr-web-discogs-2"); err != nil {
		t.Fatalf("update discogs_url: %v", err)
	}

	changes, err := lib.libraryChangesSince(v0)
	if err != nil {
		t.Fatalf("libraryChangesSince: %v", err)
	}
	if len(changes.Upserts) == 0 {
		t.Fatalf("expected upserts after update, got 0")
	}
	if changes.Upserts[0].DiscogsURL != "https://api.discogs.com/releases/888" {
		t.Fatalf("upsert discogs_url=%q want persisted URL", changes.Upserts[0].DiscogsURL)
	}
}

