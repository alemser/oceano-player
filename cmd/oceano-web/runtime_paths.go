package main

import "strings"

type runtimePaths struct {
	LibraryDB  string
	StateFile  string
	ArtworkDir string
	VUSocket   string
}

// resolveRuntimePaths loads mutable paths from config on each request so route
// handlers do not keep stale values captured at process startup.
func resolveRuntimePaths(configPath, fallbackLibraryDB string) runtimePaths {
	paths := runtimePaths{
		LibraryDB: strings.TrimSpace(fallbackLibraryDB),
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return paths
	}
	if v := strings.TrimSpace(cfg.Advanced.LibraryDB); v != "" {
		paths.LibraryDB = v
	}
	paths.StateFile = strings.TrimSpace(cfg.Advanced.StateFile)
	paths.ArtworkDir = strings.TrimSpace(cfg.Advanced.ArtworkDir)
	paths.VUSocket = strings.TrimSpace(cfg.Advanced.VUSocket)
	return paths
}
