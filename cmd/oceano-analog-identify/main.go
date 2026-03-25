package main

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type statePayload struct {
	Source         string   `json:"source"`
	Status         string   `json:"status"`
	Title          string   `json:"title"`
	Artist         string   `json:"artist"`
	Album          string   `json:"album"`
	ArtworkURL     *string  `json:"artwork_url"`
	Confidence     float64  `json:"confidence"`
	UpdatedAt      int64    `json:"updated_at"`
	PlaybackSource string   `json:"playback_source"`
	Seek           int      `json:"seek"`
	Duration       int      `json:"duration"`
	InputDevice    string   `json:"input_device"`
	FingerprintID  *string  `json:"fingerprint_id,omitempty"`
	Error          *string  `json:"error"`
}

type config struct {
	Enabled                 bool
	Device                  string
	StateFile               string
	CacheFile               string
	SignalThreshold         float64
	SilenceSeconds          int
	CaptureSeconds          int
	IdentifyIntervalSeconds int
	ConfidenceThreshold     float64
	CacheTTLSeconds         int
	AcoustIDAPIKey          string
}

type fpcalcResponse struct {
	Fingerprint string `json:"fingerprint"`
	Duration    int    `json:"duration"`
}

type acoustIDResponse struct {
	Status  string `json:"status"`
	Results []struct {
		Score      float64 `json:"score"`
		Recordings []struct {
			Title         string `json:"title"`
			Artists       []struct{ Name string `json:"name"` } `json:"artists"`
			ReleaseGroups []struct{ Title string `json:"title"` } `json:"releasegroups"`
			Releases      []struct{ ID string `json:"id"` } `json:"releases"`
		} `json:"recordings"`
	} `json:"results"`
}

type metadata struct {
	Title      string   `json:"title"`
	Artist     string   `json:"artist"`
	Album      string   `json:"album"`
	ArtworkURL *string  `json:"artwork_url"`
	Confidence float64  `json:"confidence"`
	ReleaseID  *string  `json:"release_id,omitempty"`
}

type cacheEntry struct {
	CachedAt int64    `json:"cached_at"`
	Metadata metadata `json:"metadata"`
}

var (
	acoustIDLookupURL = "https://api.acoustid.org/v2/lookup"
	itunesSearchURL   = "https://itunes.apple.com/search"
)

func envBool(name string, fallback bool) bool {
	v, ok := os.LookupEnv(name)
	if !ok {
		return fallback
	}
	n := strings.ToLower(strings.TrimSpace(v))
	return n == "1" || n == "true" || n == "yes" || n == "on"
}

func envInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(name string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func loadConfig() config {
	captureSeconds := envInt("ANALOG_CAPTURE_SECONDS", 12)
	if captureSeconds < 6 {
		captureSeconds = 6
	}
	identifyInterval := envInt("ANALOG_IDENTIFY_INTERVAL_SECONDS", 45)
	if identifyInterval < 20 {
		identifyInterval = 20
	}
	cacheTTL := envInt("ANALOG_CACHE_TTL_SECONDS", 86400)
	if cacheTTL < 60 {
		cacheTTL = 60
	}
	return config{
		Enabled:                 envBool("ANALOG_INPUT_ENABLED", true),
		Device:                  firstNonEmpty(os.Getenv("ANALOG_INPUT_DEVICE"), os.Getenv("ALSA_DEVICE"), "hw:1,0"),
		StateFile:               firstNonEmpty(os.Getenv("ANALOG_METADATA_FILE"), "/run/oceano-player/analog-now-playing.json"),
		CacheFile:               firstNonEmpty(os.Getenv("ANALOG_CACHE_FILE"), "/var/lib/oceano-player/analog-cache.json"),
		SignalThreshold:         envFloat("ANALOG_INPUT_THRESHOLD", 0.01),
		SilenceSeconds:          envInt("ANALOG_SILENCE_SECONDS", 6),
		CaptureSeconds:          captureSeconds,
		IdentifyIntervalSeconds: identifyInterval,
		ConfidenceThreshold:     envFloat("ANALOG_CONFIDENCE_THRESHOLD", 0.80),
		CacheTTLSeconds:         cacheTTL,
		AcoustIDAPIKey:          strings.TrimSpace(os.Getenv("ACOUSTID_API_KEY")),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func atomicWriteJSON(path string, payload any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "analog-now-playing-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	enc := json.NewEncoder(tmp)
	if err := enc.Encode(payload); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func runCommand(timeout time.Duration, name string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout, exitErr.Stderr, err
	}
	return stdout, nil, err
}

func sampleRMS(device string) (float64, error) {
	stdout, stderr, err := runCommand(7*time.Second,
		"arecord", "-q", "-D", device,
		"-f", "S16_LE", "-c", "1", "-r", "44100", "-d", "1", "-t", "raw",
	)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return 0.0, errors.New(msg)
	}

	sampleCount := len(stdout) / 2
	if sampleCount == 0 {
		return 0.0, nil
	}

	var total float64
	for i := 0; i < sampleCount; i++ {
		s := int16(binary.LittleEndian.Uint16(stdout[i*2 : i*2+2]))
		v := float64(s) / 32768.0
		total += v * v
	}
	return math.Sqrt(total / float64(sampleCount)), nil
}

// probeDevice attempts a 1-second capture to /dev/null to verify the device
// is a functional capture device. Using arecord ensures the device supports
// capture and is not a playback-only interface (e.g. the AirPlay output).
func probeDevice(device string) error {
	_, stderr, err := runCommand(5*time.Second,
		"arecord", "-q", "-D", device,
		"-f", "S16_LE", "-c", "1", "-r", "44100", "-d", "1", "-t", "raw", os.DevNull,
	)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// waitForDevice blocks until device is available (probe succeeds) or a signal
// is received. Retries with exponential backoff from 5 s up to 60 s between
// probes to avoid resource pressure. Returns true when ready, false on exit signal.
func waitForDevice(device string, sig <-chan os.Signal) bool {
	interval := 5 * time.Second
	for {
		select {
		case <-sig:
			return false
		case <-time.After(interval):
		}
		if probeDevice(device) == nil {
			log.Printf("[oceano-analog] capture device %q is now ready", device)
			return true
		}
		if interval < 60*time.Second {
			interval *= 2
			if interval > 60*time.Second {
				interval = 60 * time.Second
			}
		}
	}
}

func captureWAV(device string, seconds int, dst string) error {
	_, stderr, err := runCommand(time.Duration(seconds+8)*time.Second,
		"arecord", "-q", "-D", device,
		"-f", "S16_LE", "-c", "1", "-r", "44100", "-d", strconv.Itoa(seconds), "-t", "wav", dst,
	)
	if err != nil {
		return fmt.Errorf("capture wav failed: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	info, statErr := os.Stat(dst)
	if statErr != nil || info.Size() <= 44 {
		return fmt.Errorf("capture wav output invalid")
	}
	return nil
}

func fingerprint(wavPath string) (string, int, error) {
	stdout, stderr, err := runCommand(20*time.Second, "fpcalc", "-json", wavPath)
	if err != nil {
		return "", 0, fmt.Errorf("fpcalc failed: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	var resp fpcalcResponse
	if unmarshalErr := json.Unmarshal(stdout, &resp); unmarshalErr != nil {
		return "", 0, unmarshalErr
	}
	if strings.TrimSpace(resp.Fingerprint) == "" || resp.Duration <= 0 {
		return "", 0, errors.New("fpcalc returned empty fingerprint")
	}
	return resp.Fingerprint, resp.Duration, nil
}

func acoustIDLookup(apiKey, fp string, duration int) (*metadata, error) {
	query := url.Values{}
	query.Set("client", apiKey)
	query.Set("format", "json")
	query.Set("meta", "recordings+releasegroups+releases")
	query.Set("duration", strconv.Itoa(duration))
	query.Set("fingerprint", fp)

	u := acoustIDLookupURL + "?" + query.Encode()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("acoustid status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload acoustIDResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Status != "ok" {
		return nil, errors.New("acoustid non-ok status")
	}

	var best *metadata
	bestScore := -1.0
	for _, result := range payload.Results {
		if len(result.Recordings) == 0 {
			continue
		}
		rec := result.Recordings[0]
		m := &metadata{
			Title:      firstNonEmpty(strings.TrimSpace(rec.Title), "Unknown"),
			Artist:     "Unknown",
			Album:      "Unknown",
			Confidence: result.Score,
		}
		if len(rec.Artists) > 0 {
			m.Artist = firstNonEmpty(strings.TrimSpace(rec.Artists[0].Name), "Unknown")
		}
		if len(rec.ReleaseGroups) > 0 {
			m.Album = firstNonEmpty(strings.TrimSpace(rec.ReleaseGroups[0].Title), "Unknown")
		}
		if len(rec.Releases) > 0 {
			releaseID := strings.TrimSpace(rec.Releases[0].ID)
			if releaseID != "" {
				m.ReleaseID = &releaseID
			}
		}
		if result.Score > bestScore {
			bestScore = result.Score
			best = m
		}
	}
	if best == nil {
		return nil, errors.New("no acoustid matches")
	}
	return best, nil
}

func withArtworkURL(in *metadata) *metadata {
	if in == nil {
		return nil
	}
	out := *in
	if in.ReleaseID != nil && strings.TrimSpace(*in.ReleaseID) != "" {
		u := fmt.Sprintf("https://coverartarchive.org/release/%s/front-250", url.PathEscape(*in.ReleaseID))
		out.ArtworkURL = &u
		return &out
	}

	term := strings.TrimSpace(strings.Join([]string{in.Artist, in.Album}, " "))
	if term == "" {
		return &out
	}
	query := url.Values{}
	query.Set("term", term)
	query.Set("entity", "album")
	query.Set("limit", "1")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(itunesSearchURL + "?" + query.Encode())
	if err != nil {
		return &out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &out
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &out
	}
	var payload struct {
		Results []struct {
			ArtworkURL100 string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return &out
	}
	if len(payload.Results) > 0 {
		art := strings.TrimSpace(payload.Results[0].ArtworkURL100)
		if art != "" {
			art = strings.ReplaceAll(art, "100x100bb", "600x600bb")
			out.ArtworkURL = &art
		}
	}
	return &out
}

func loadCache(path string) map[string]cacheEntry {
	payload := map[string]cacheEntry{}
	data, err := os.ReadFile(path)
	if err != nil {
		return payload
	}
	_ = json.Unmarshal(data, &payload)
	return payload
}

func saveCache(path string, payload map[string]cacheEntry) {
	_ = atomicWriteJSON(path, payload)
}

func ptrString(v string) *string {
	value := v
	return &value
}

func setError(state *statePayload, value string) {
	if strings.TrimSpace(value) == "" {
		state.Error = nil
		return
	}
	state.Error = ptrString(value)
}

func main() {
	cfg := loadConfig()
	if !cfg.Enabled {
		return
	}

	_ = os.MkdirAll(filepath.Dir(cfg.StateFile), 0o755)
	_ = os.MkdirAll(filepath.Dir(cfg.CacheFile), 0o755)

	state := statePayload{
		Source:         "analog",
		Status:         "idle",
		Title:          "Unknown",
		Artist:         "Unknown",
		Album:          "Unknown",
		Confidence:     0.0,
		UpdatedAt:      time.Now().Unix(),
		PlaybackSource: "Analog",
		Seek:           0,
		Duration:       0,
		InputDevice:    cfg.Device,
		Error:          nil,
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	// Probe the capture device before starting. If missing or not capture-capable
	// (e.g. the AirPlay playback output was accidentally configured), log clearly
	// and wait with backoff until available or a shutdown signal arrives.
	if err := probeDevice(cfg.Device); err != nil {
		hint := ""
		if strings.Contains(strings.ToLower(err.Error()), "invalid argument") {
			hint = " (device may be playback-only — verify ANALOG_INPUT_DEVICE is not the AirPlay output)"
		}
		log.Printf("[oceano-analog] capture device %q not available: %v%s", cfg.Device, err, hint)
		state.Status = "error"
		setError(&state, "device_not_found")
		state.UpdatedAt = time.Now().Unix()
		_ = atomicWriteJSON(cfg.StateFile, state)
		if !waitForDevice(cfg.Device, sig) {
			return
		}
		state.Status = "idle"
		setError(&state, "")
		state.UpdatedAt = time.Now().Unix()
	}
	_ = atomicWriteJSON(cfg.StateFile, state)

	cache := loadCache(cfg.CacheFile)

	lastSignal := time.Now()
	lastIdentify := time.Time{}
	ticker := time.NewTicker(1 * time.Second)
	defer func() { ticker.Stop() }()
	consecutiveErrors := 0

	for {
		select {
		case <-sig:
			return
		case <-ticker.C:
			rms, sampleErr := sampleRMS(cfg.Device)

			if sampleErr != nil {
				consecutiveErrors++
				if consecutiveErrors < 3 {
					continue
				}
				log.Printf("[oceano-analog] capture device %q lost: %v; waiting for reconnect", cfg.Device, sampleErr)
				consecutiveErrors = 0
				ticker.Stop()
				state.Status = "error"
				state.Title = "Unknown"
				state.Artist = "Unknown"
				state.Album = "Unknown"
				state.ArtworkURL = nil
				state.FingerprintID = nil
				state.Confidence = 0.0
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "device_not_found")
				_ = atomicWriteJSON(cfg.StateFile, state)
				if !waitForDevice(cfg.Device, sig) {
					return
				}
				state.Status = "idle"
				setError(&state, "")
				state.UpdatedAt = time.Now().Unix()
				_ = atomicWriteJSON(cfg.StateFile, state)
				lastSignal = time.Now()
				lastIdentify = time.Time{}
				ticker = time.NewTicker(1 * time.Second)
				continue
			}
			consecutiveErrors = 0

			hasSignal := rms >= cfg.SignalThreshold
			now := time.Now()

			if hasSignal {
				lastSignal = now
			}

			if hasSignal && state.Status == "idle" {
				state.Status = "playing"
				state.UpdatedAt = now.Unix()
				setError(&state, "")
				_ = atomicWriteJSON(cfg.StateFile, state)
			}

			if !hasSignal && state.Status != "idle" && int(now.Sub(lastSignal).Seconds()) >= cfg.SilenceSeconds {
				state.Status = "idle"
				state.Title = "Unknown"
				state.Artist = "Unknown"
				state.Album = "Unknown"
				state.ArtworkURL = nil
				state.FingerprintID = nil
				state.Confidence = 0.0
				state.UpdatedAt = now.Unix()
				setError(&state, "")
				_ = atomicWriteJSON(cfg.StateFile, state)
				continue
			}

			if !hasSignal || (!lastIdentify.IsZero() && int(now.Sub(lastIdentify).Seconds()) < cfg.IdentifyIntervalSeconds) {
				continue
			}
			lastIdentify = now
			state.Status = "identifying"
			state.UpdatedAt = now.Unix()
			setError(&state, "")
			_ = atomicWriteJSON(cfg.StateFile, state)

			tmpDir, err := os.MkdirTemp("", "oceano-analog-")
			if err != nil {
				state.Status = "playing"
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "tmpdir_failed")
				_ = atomicWriteJSON(cfg.StateFile, state)
				continue
			}
			wav := filepath.Join(tmpDir, "sample.wav")
			if err := captureWAV(cfg.Device, cfg.CaptureSeconds, wav); err != nil {
				_ = os.RemoveAll(tmpDir)
				state.Status = "playing"
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "capture_failed")
				_ = atomicWriteJSON(cfg.StateFile, state)
				continue
			}
			fp, duration, err := fingerprint(wav)
			_ = os.RemoveAll(tmpDir)
			if err != nil {
				state.Status = "playing"
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "fingerprint_failed")
				_ = atomicWriteJSON(cfg.StateFile, state)
				continue
			}

			h := sha1.Sum([]byte(fp))
			fpKey := fmt.Sprintf("%x", h[:])
			var found *metadata
			if entry, ok := cache[fpKey]; ok {
				if int(now.Unix()-entry.CachedAt) <= cfg.CacheTTLSeconds {
					m := entry.Metadata
					found = &m
				}
			}

			if found == nil && cfg.AcoustIDAPIKey != "" {
				m, err := acoustIDLookup(cfg.AcoustIDAPIKey, fp, duration)
				if err == nil && m.Confidence >= cfg.ConfidenceThreshold {
					found = withArtworkURL(m)
					cache[fpKey] = cacheEntry{CachedAt: time.Now().Unix(), Metadata: *found}
					saveCache(cfg.CacheFile, cache)
				}
			}

			if found != nil {
				state.Status = "playing_with_metadata"
				state.Title = found.Title
				state.Artist = found.Artist
				state.Album = found.Album
				state.ArtworkURL = found.ArtworkURL
				state.Confidence = found.Confidence
				state.FingerprintID = &fpKey
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "")
			} else {
				state.Status = "playing"
				state.UpdatedAt = time.Now().Unix()
				setError(&state, "lookup_failed")
			}
			_ = atomicWriteJSON(cfg.StateFile, state)
		}
	}
}
