package main

import (
	"encoding/json"
	"net/http"
	"os"
)

var restartDisplayServiceFn = func() error {
	return restartService(displayUnit)
}

var displayServicePathFn = func() string {
	return "/etc/systemd/system/" + displayUnit
}

func handleDisplayServiceRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	svcPath := displayServicePathFn()
	if _, err := os.Stat(svcPath); err != nil {
		http.Error(w, "display service not installed", http.StatusNotFound)
		return
	}

	if err := restartDisplayServiceFn(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "restarted"})
}
