package recognition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const auddEndpoint = "https://api.audd.io/"

// AudDConfig holds the API token for the AudD standard recognition endpoint.
// See https://docs.audd.io/
type AudDConfig struct {
	APIToken string
}

// AudDRecognizer implements Recognizer using the documented AudD REST API
// (multipart file upload). Commercially safe: official BYOK token from dashboard.audd.io.
type AudDRecognizer struct {
	cfg    AudDConfig
	client *http.Client
}

// NewAudDRecognizer returns a recognizer that POSTs WAV files to api.audd.io,
// or nil when APIToken is empty (caller should omit from chain).
func NewAudDRecognizer(cfg AudDConfig) Recognizer {
	if strings.TrimSpace(cfg.APIToken) == "" {
		return nil
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
	}
	return &AudDRecognizer{
		cfg: AudDConfig{APIToken: strings.TrimSpace(cfg.APIToken)},
		client: &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}
}

func (r *AudDRecognizer) Name() string { return "AudD" }

func (r *AudDRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := writeFields(mw, map[string]string{
		"api_token": r.cfg.APIToken,
		// Extra metadata for ISRC / MusicBrainz recording id when available.
		"return": "musicbrainz",
	}); err != nil {
		return nil, err
	}
	fw, err := mw.CreateFormFile("file", filepath.Base(wavPath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, auddEndpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimit
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("AudD: read body: %w", err)
	}

	return parseAudDResponse(raw, resp.StatusCode)
}

func parseAudDResponse(body []byte, httpStatus int) (*Result, error) {
	var env struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("AudD: decode envelope: %w", err)
	}

	if env.Status == "error" {
		if code, msg, ok := parseAudDErrorPayload(env.Error); ok {
			if isAudDRateLimitCode(code) || strings.Contains(strings.ToLower(msg), "limit") {
				return nil, ErrRateLimit
			}
			return nil, fmt.Errorf("AudD error %d: %s", code, msg)
		}
		if len(env.Error) > 0 {
			return nil, fmt.Errorf("AudD error: %s", strings.TrimSpace(string(env.Error)))
		}
		return nil, fmt.Errorf("AudD error (http %d)", httpStatus)
	}

	if env.Status != "success" {
		return nil, fmt.Errorf("AudD: unexpected status %q", env.Status)
	}

	if len(env.Result) == 0 || string(env.Result) == "null" {
		return nil, nil
	}

	var hit auddHit
	if err := json.Unmarshal(env.Result, &hit); err != nil {
		return nil, fmt.Errorf("AudD: decode result: %w", err)
	}
	if strings.TrimSpace(hit.Title) == "" && strings.TrimSpace(hit.Artist) == "" {
		return nil, nil
	}

	res := &Result{
		Title:       strings.TrimSpace(hit.Title),
		Artist:      strings.TrimSpace(hit.Artist),
		Album:       strings.TrimSpace(hit.Album),
		Label:       strings.TrimSpace(hit.Label),
		Released:    strings.TrimSpace(hit.ReleaseDate),
		MatchSource: "audd",
	}
	if hit.Score != nil {
		res.Score = *hit.Score
	}
	unmarshalAudDMusicbrainz(env.Result, res)
	return res, nil
}

func unmarshalAudDMusicbrainz(resultJSON json.RawMessage, res *Result) {
	var wrap struct {
		Musicbrainz json.RawMessage `json:"musicbrainz"`
	}
	if err := json.Unmarshal(resultJSON, &wrap); err != nil || len(wrap.Musicbrainz) == 0 {
		return
	}
	var single auddMusicBrainz
	if json.Unmarshal(wrap.Musicbrainz, &single) == nil && (single.ISRC != "" || single.RecordingID != "") {
		if res.ISRC == "" {
			res.ISRC = strings.TrimSpace(single.ISRC)
		}
		return
	}
	var list []auddMusicBrainz
	if json.Unmarshal(wrap.Musicbrainz, &list) != nil {
		return
	}
	for _, mb := range list {
		if strings.TrimSpace(mb.ISRC) != "" && res.ISRC == "" {
			res.ISRC = strings.TrimSpace(mb.ISRC)
			break
		}
	}
}

type auddHit struct {
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	Album       string `json:"album"`
	ReleaseDate string `json:"release_date"`
	Label       string `json:"label"`
	Timecode    string `json:"timecode"`
	SongLink    string `json:"song_link"`
	Score       *int   `json:"score"`
}

type auddMusicBrainz struct {
	ISRC        string `json:"isrc"`
	RecordingID string `json:"id"`
}

func parseAudDErrorPayload(raw json.RawMessage) (code int, msg string, ok bool) {
	if len(raw) == 0 {
		return 0, "", false
	}
	var obj struct {
		Code          int    `json:"code"`
		ErrorCode     int    `json:"error_code"`
		Message       string `json:"message"`
		ErrorMessage  string `json:"error_message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		c := obj.Code
		if c == 0 {
			c = obj.ErrorCode
		}
		m := obj.Message
		if m == "" {
			m = obj.ErrorMessage
		}
		if c != 0 || m != "" {
			return c, m, true
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return 0, s, true
	}
	return 0, string(raw), true
}

func isAudDRateLimitCode(code int) bool {
	// https://docs.audd.io — #901 when token missing and limit reached; map similar quota codes as discovered.
	return code == 901
}
