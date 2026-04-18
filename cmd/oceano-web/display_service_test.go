package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHandleDisplayServiceRestart_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/display/restart", nil)
	w := httptest.NewRecorder()

	handleDisplayServiceRestart(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", w.Code)
	}
}

func TestHandleDisplayServiceRestart_NotInstalled(t *testing.T) {
	origPathFn := displayServicePathFn
	displayServicePathFn = func() string { return t.TempDir() + "/missing.service" }
	t.Cleanup(func() { displayServicePathFn = origPathFn })

	req := httptest.NewRequest(http.MethodPost, "/api/display/restart", nil)
	w := httptest.NewRecorder()

	handleDisplayServiceRestart(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleDisplayServiceRestart_OK(t *testing.T) {
	tempDir := t.TempDir()
	origRestartFn := restartDisplayServiceFn
	origPathFn := displayServicePathFn
	t.Cleanup(func() {
		restartDisplayServiceFn = origRestartFn
		displayServicePathFn = origPathFn
	})

	svcPath := tempDir + "/oceano-display.service"
	displayServicePathFn = func() string { return svcPath }
	if err := os.WriteFile(svcPath, []byte("[Unit]\nDescription=test\n"), 0o644); err != nil {
		t.Fatalf("write service file: %v", err)
	}

	called := false
	restartDisplayServiceFn = func() error {
		called = true
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/display/restart", nil)
	w := httptest.NewRecorder()

	handleDisplayServiceRestart(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if !called {
		t.Fatal("expected restart function to be called")
	}
	if got := w.Body.String(); got == "" {
		t.Fatal("expected JSON response body")
	}
}
