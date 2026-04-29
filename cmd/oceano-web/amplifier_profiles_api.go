package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const amplifierProfileSchemaVersion = "1.0"

type amplifierProfilesListResponse struct {
	ActiveProfileID string                   `json:"active_profile_id"`
	Profiles        []StoredAmplifierProfile `json:"profiles"`
}

type amplifierProfileExportDoc struct {
	SchemaVersion string                 `json:"schema_version"`
	Profile       StoredAmplifierProfile `json:"profile"`
}

func (s *amplifierServer) handleAmplifierProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAmplifierProfilesList(w)
	case http.MethodPost:
		s.handleAmplifierProfilesUpsert(w, r)
	case http.MethodDelete:
		s.handleAmplifierProfilesDelete(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *amplifierServer) handleAmplifierProfilesList(w http.ResponseWriter) {
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}
	profiles := make([]StoredAmplifierProfile, 0, len(cfg.AmplifierProfiles)+len(builtInAmplifierProfileIDs()))
	for _, id := range builtInAmplifierProfileIDs() {
		if pCfg, ok := builtInAmplifierProfile(id); ok {
			profiles = append(profiles, StoredAmplifierProfile{
				ID:     id,
				Name:   pCfg.Maker + " " + pCfg.Model,
				Origin: "builtin",
				Config: pCfg,
			})
		}
	}
	profiles = append(profiles, cfg.AmplifierProfiles...)
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})

	jsonOK(w, amplifierProfilesListResponse{
		ActiveProfileID: inferActiveAmplifierProfileID(cfg.Amplifier),
		Profiles:        profiles,
	})
}

func (s *amplifierServer) handleAmplifierProfilesUpsert(w http.ResponseWriter, r *http.Request) {
	var req StoredAmplifierProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	if req.ID == "" {
		jsonError(w, "profile id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = req.ID
	}
	if _, ok := builtInAmplifierProfile(req.ID); ok {
		jsonError(w, "cannot overwrite built-in profile", http.StatusBadRequest)
		return
	}
	req.Config = normalizeAmplifierInputs(req.Config)
	if err := validateStoredAmplifierProfile(req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Origin == "" || req.Origin == "builtin" {
		req.Origin = "custom"
	}
	req.Config.ProfileID = req.ID

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	_, idx, found := findStoredAmplifierProfile(cfg.AmplifierProfiles, req.ID)
	if found {
		cfg.AmplifierProfiles[idx] = req
	} else {
		cfg.AmplifierProfiles = append(cfg.AmplifierProfiles, req)
	}

	if err := saveConfig(s.configPath, cfg); err != nil {
		jsonError(w, "failed to save config", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"ok": true, "profile_id": req.ID, "updated": found})
}

func (s *amplifierServer) handleAmplifierProfilesDelete(w http.ResponseWriter, r *http.Request) {
	profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
	if profileID == "" {
		jsonError(w, "profile_id is required", http.StatusBadRequest)
		return
	}
	if _, ok := builtInAmplifierProfile(profileID); ok {
		jsonError(w, "cannot delete built-in profile", http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}
	active := inferActiveAmplifierProfileID(cfg.Amplifier)
	if strings.EqualFold(active, profileID) {
		jsonError(w, "cannot delete active profile", http.StatusBadRequest)
		return
	}

	_, idx, found := findStoredAmplifierProfile(cfg.AmplifierProfiles, profileID)
	if !found {
		jsonError(w, "profile not found", http.StatusNotFound)
		return
	}
	cfg.AmplifierProfiles = append(cfg.AmplifierProfiles[:idx], cfg.AmplifierProfiles[idx+1:]...)

	if err := saveConfig(s.configPath, cfg); err != nil {
		jsonError(w, "failed to save config", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"ok": true, "profile_id": profileID})
}

func (s *amplifierServer) handleAmplifierProfileActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ProfileID string `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	if req.ProfileID == "" {
		jsonError(w, "profile_id is required", http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	if pCfg, ok := builtInAmplifierProfile(req.ProfileID); ok {
		preserved := cfg.Amplifier
		cfg.Amplifier = pCfg
		cfg.Amplifier.ProfileID = req.ProfileID
		cfg.Amplifier.Enabled = preserved.Enabled
		// Merge built-in IR defaults with any learned overrides so activating a
		// built-in profile keeps defaults while still preserving user learning.
		mergedCodes := map[string]string{}
		for k, v := range cfg.Amplifier.IRCodes {
			mergedCodes[k] = v
		}
		for k, v := range preserved.IRCodes {
			if strings.TrimSpace(v) == "" {
				continue
			}
			mergedCodes[k] = v
		}
		cfg.Amplifier.IRCodes = mergedCodes
		// Preserve user-configured devices because built-ins do not include
		// device-specific remotes (CD players, etc).
		cfg.Amplifier.ConnectedDevices = preserved.ConnectedDevices
		if preserved.Broadlink.Host != "" {
			cfg.Amplifier.Broadlink.Host = preserved.Broadlink.Host
		}
		if preserved.Broadlink.Port != 0 {
			cfg.Amplifier.Broadlink.Port = preserved.Broadlink.Port
		}
		if preserved.Broadlink.Token != "" {
			cfg.Amplifier.Broadlink.Token = preserved.Broadlink.Token
		}
		if preserved.Broadlink.DeviceID != "" {
			cfg.Amplifier.Broadlink.DeviceID = preserved.Broadlink.DeviceID
		}
	} else {
		stored, _, ok := findStoredAmplifierProfile(cfg.AmplifierProfiles, req.ProfileID)
		if !ok {
			jsonError(w, "profile not found", http.StatusNotFound)
			return
		}
		preserved := cfg.Amplifier
		cfg.Amplifier = resolveAmplifierConfig(stored.Config)
		cfg.Amplifier.ProfileID = req.ProfileID
		cfg.Amplifier.Enabled = preserved.Enabled
		// If the stored profile has no connected devices (e.g. cloned from built-in),
		// fall back to what the user had configured so devices are not lost.
		if len(cfg.Amplifier.ConnectedDevices) == 0 {
			cfg.Amplifier.ConnectedDevices = preserved.ConnectedDevices
		}
		if cfg.Amplifier.Broadlink.Host == "" {
			cfg.Amplifier.Broadlink.Host = preserved.Broadlink.Host
		}
		if cfg.Amplifier.Broadlink.Port == 0 {
			cfg.Amplifier.Broadlink.Port = preserved.Broadlink.Port
		}
		if cfg.Amplifier.Broadlink.Token == "" {
			cfg.Amplifier.Broadlink.Token = preserved.Broadlink.Token
		}
		if cfg.Amplifier.Broadlink.DeviceID == "" {
			cfg.Amplifier.Broadlink.DeviceID = preserved.Broadlink.DeviceID
		}
	}

	if err := saveConfig(s.configPath, cfg); err != nil {
		jsonError(w, "failed to save config", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"ok": true, "active_profile_id": req.ProfileID})
}

func (s *amplifierServer) handleAmplifierProfileExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	profileID := strings.TrimSpace(r.URL.Query().Get("profile_id"))
	if profileID == "" {
		jsonError(w, "profile_id is required", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	if mode == "" {
		mode = "safe"
	}
	if mode != "safe" && mode != "full" {
		jsonError(w, "mode must be safe or full", http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	var profile StoredAmplifierProfile
	if pCfg, ok := builtInAmplifierProfile(profileID); ok {
		profile = StoredAmplifierProfile{
			ID:     profileID,
			Name:   pCfg.Maker + " " + pCfg.Model,
			Origin: "builtin",
			Config: pCfg,
		}
	} else {
		stored, _, ok := findStoredAmplifierProfile(cfg.AmplifierProfiles, profileID)
		if !ok {
			jsonError(w, "profile not found", http.StatusNotFound)
			return
		}
		profile = stored
	}

	if mode == "safe" {
		profile.Config.Broadlink.Token = ""
		profile.Config.Broadlink.DeviceID = ""
	}

	jsonOK(w, amplifierProfileExportDoc{
		SchemaVersion: amplifierProfileSchemaVersion,
		Profile:       profile,
	})
}

func (s *amplifierServer) handleAmplifierProfileImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var doc amplifierProfileExportDoc
	if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(doc.SchemaVersion) != amplifierProfileSchemaVersion {
		jsonError(w, "unsupported schema_version", http.StatusBadRequest)
		return
	}
	doc.Profile.ID = strings.TrimSpace(doc.Profile.ID)
	doc.Profile.Name = strings.TrimSpace(doc.Profile.Name)
	if doc.Profile.ID == "" || doc.Profile.Name == "" {
		jsonError(w, "profile id and name are required", http.StatusBadRequest)
		return
	}
	if _, ok := builtInAmplifierProfile(doc.Profile.ID); ok {
		jsonError(w, "cannot import over built-in profile id", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(doc.Profile.Config.Maker) == "" || strings.TrimSpace(doc.Profile.Config.Model) == "" {
		jsonError(w, "profile maker/model are required", http.StatusBadRequest)
		return
	}

	doc.Profile.Origin = "imported"
	doc.Profile.Config.ProfileID = doc.Profile.ID
	doc.Profile.Config = normalizeAmplifierInputs(doc.Profile.Config)
	if err := validateStoredAmplifierProfile(doc.Profile); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := loadConfig(s.configPath)
	if err != nil {
		jsonError(w, "failed to load config", http.StatusInternalServerError)
		return
	}

	_, idx, found := findStoredAmplifierProfile(cfg.AmplifierProfiles, doc.Profile.ID)
	if found {
		cfg.AmplifierProfiles[idx] = doc.Profile
	} else {
		cfg.AmplifierProfiles = append(cfg.AmplifierProfiles, doc.Profile)
	}

	if err := saveConfig(s.configPath, cfg); err != nil {
		jsonError(w, "failed to save config", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{"ok": true, "profile_id": doc.Profile.ID})
}

func validateStoredAmplifierProfile(p StoredAmplifierProfile) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("profile id is required")
	}
	if strings.TrimSpace(p.Config.Maker) == "" || strings.TrimSpace(p.Config.Model) == "" {
		return fmt.Errorf("profile maker/model are required")
	}
	if len(p.Config.Inputs) == 0 {
		return fmt.Errorf("at least one input is required")
	}
	seen := map[AmplifierInputID]struct{}{}
	for i, in := range p.Config.Inputs {
		if strings.TrimSpace(string(in.ID)) == "" {
			return fmt.Errorf("input %d has empty id", i)
		}
		if strings.TrimSpace(in.LogicalName) == "" {
			return fmt.Errorf("input %d has empty logical_name", i)
		}
		if _, ok := seen[in.ID]; ok {
			return fmt.Errorf("duplicate input id %q", in.ID)
		}
		seen[in.ID] = struct{}{}
	}
	inputMode := strings.TrimSpace(p.Config.InputMode)
	if inputMode == "" {
		inputMode = "cycle"
	}
	if inputMode != "cycle" && inputMode != "direct" {
		return fmt.Errorf("input_mode must be cycle or direct")
	}
	if inputMode == "direct" {
		for _, in := range p.Config.Inputs {
			key := "input_" + string(in.ID)
			if strings.TrimSpace(p.Config.IRCodes[key]) == "" {
				return fmt.Errorf("direct mode requires IR code %q for every registered input", key)
			}
		}
	}
	return nil
}
