package main

import "strings"

const builtInAmplifierProfileMagnatMR780 = "magnat_mr780"

func builtInAmplifierProfileIDs() []string {
	return []string{builtInAmplifierProfileMagnatMR780}
}

func builtInAmplifierProfile(id string) (AmplifierConfig, bool) {
	switch strings.TrimSpace(strings.ToLower(id)) {
	case builtInAmplifierProfileMagnatMR780:
		return AmplifierConfig{
			ProfileID:          builtInAmplifierProfileMagnatMR780,
			InputMode:          "cycle",
			Maker:              "Magnat",
			Model:              "MR 780",
			WarmUpSecs:         30,
			StandbyTimeoutMins: 20,
			Inputs: []AmplifierInputConfig{
				{ID: AmplifierInputID("10"), LogicalName: "Phono", Visible: false},
				{ID: AmplifierInputID("20"), LogicalName: "CD", Visible: true},
				{ID: AmplifierInputID("30"), LogicalName: "Aux", Visible: true},
				{ID: AmplifierInputID("40"), LogicalName: "USB Audio", Visible: true},
			},
			InputCycling: InputCyclingConfig{
				Enabled:        false,
				Direction:      "prev",
				MaxCycles:      8,
				StepWaitSecs:   3,
				MinSilenceSecs: 120,
			},
			USBReset: USBResetConfig{
				MaxAttempts:       13,
				FirstStepSettleMS: 150,
				StepWaitMS:        2400,
			},
		}, true
	default:
		return AmplifierConfig{}, false
	}
}

// resolveAmplifierConfig merges profile defaults into cfg while preserving
// explicit legacy values to keep current installations behavior-compatible.
func resolveAmplifierConfig(cfg AmplifierConfig) AmplifierConfig {
	profileID := strings.TrimSpace(cfg.ProfileID)
	if profileID == "" {
		if strings.EqualFold(strings.TrimSpace(cfg.Maker), "Magnat") && strings.EqualFold(strings.TrimSpace(cfg.Model), "MR 780") {
			profileID = builtInAmplifierProfileMagnatMR780
		} else {
			return normalizeAmplifierInputs(cfg)
		}
	}

	base, ok := builtInAmplifierProfile(profileID)
	if !ok {
		return normalizeAmplifierInputs(cfg)
	}

	out := base
	out.ProfileID = profileID

	// Explicit values override profile defaults.
	out.Enabled = cfg.Enabled
	if cfg.InputMode != "" {
		out.InputMode = cfg.InputMode
	}
	if cfg.Maker != "" {
		out.Maker = cfg.Maker
	}
	if cfg.Model != "" {
		out.Model = cfg.Model
	}
	if cfg.WarmUpSecs > 0 {
		out.WarmUpSecs = cfg.WarmUpSecs
	}
	if cfg.StandbyTimeoutMins > 0 {
		out.StandbyTimeoutMins = cfg.StandbyTimeoutMins
	}
	if len(cfg.Inputs) > 0 {
		out.Inputs = cfg.Inputs
	}
	if cfg.Broadlink.Host != "" {
		out.Broadlink.Host = cfg.Broadlink.Host
	}
	if cfg.Broadlink.Port != 0 {
		out.Broadlink.Port = cfg.Broadlink.Port
	}
	if cfg.Broadlink.Token != "" {
		out.Broadlink.Token = cfg.Broadlink.Token
	}
	if cfg.Broadlink.DeviceID != "" {
		out.Broadlink.DeviceID = cfg.Broadlink.DeviceID
	}

	if cfg.IRCodes != nil {
		mergedCodes := map[string]string{}
		for k, v := range out.IRCodes {
			mergedCodes[k] = v
		}
		for k, v := range cfg.IRCodes {
			mergedCodes[k] = v
		}
		out.IRCodes = mergedCodes
	}

	if cfg.InputCycling.Direction != "" {
		out.InputCycling.Direction = cfg.InputCycling.Direction
	}
	out.InputCycling.Enabled = cfg.InputCycling.Enabled
	if cfg.InputCycling.MaxCycles > 0 {
		out.InputCycling.MaxCycles = cfg.InputCycling.MaxCycles
	}
	if cfg.InputCycling.StepWaitSecs > 0 {
		out.InputCycling.StepWaitSecs = cfg.InputCycling.StepWaitSecs
	}
	if cfg.InputCycling.MinSilenceSecs > 0 {
		out.InputCycling.MinSilenceSecs = cfg.InputCycling.MinSilenceSecs
	}

	if cfg.USBReset.MaxAttempts > 0 {
		out.USBReset.MaxAttempts = cfg.USBReset.MaxAttempts
	}
	if cfg.USBReset.FirstStepSettleMS > 0 {
		out.USBReset.FirstStepSettleMS = cfg.USBReset.FirstStepSettleMS
	}
	if cfg.USBReset.StepWaitMS > 0 {
		out.USBReset.StepWaitMS = cfg.USBReset.StepWaitMS
	}

	return normalizeAmplifierInputs(out)
}

func normalizeAmplifierInputs(cfg AmplifierConfig) AmplifierConfig {
	if cfg.InputMode == "" {
		cfg.InputMode = "cycle"
	}
	if len(cfg.Inputs) == 0 {
		return cfg
	}
	norm := make([]AmplifierInputConfig, 0, len(cfg.Inputs))
	seen := map[AmplifierInputID]struct{}{}
	for _, in := range cfg.Inputs {
		if strings.TrimSpace(string(in.ID)) == "" {
			continue
		}
		if _, ok := seen[in.ID]; ok {
			continue
		}
		seen[in.ID] = struct{}{}
		if strings.TrimSpace(in.LogicalName) == "" {
			in.LogicalName = string(in.ID)
		}
		norm = append(norm, in)
	}
	cfg.Inputs = norm
	return cfg
}

func inferActiveAmplifierProfileID(cfg AmplifierConfig) string {
	if strings.TrimSpace(cfg.ProfileID) != "" {
		return strings.TrimSpace(cfg.ProfileID)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Maker), "Magnat") && strings.EqualFold(strings.TrimSpace(cfg.Model), "MR 780") {
		return builtInAmplifierProfileMagnatMR780
	}
	return ""
}

func findStoredAmplifierProfile(profiles []StoredAmplifierProfile, id string) (StoredAmplifierProfile, int, bool) {
	id = strings.TrimSpace(id)
	for i, p := range profiles {
		if strings.EqualFold(strings.TrimSpace(p.ID), id) {
			return p, i, true
		}
	}
	return StoredAmplifierProfile{}, -1, false
}
