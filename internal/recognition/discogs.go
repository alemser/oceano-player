package recognition

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const discogsDefaultBaseURL = "https://api.discogs.com"

type DiscogsClientConfig struct {
	Token      string
	Timeout    time.Duration
	MaxRetries int
	UserAgent  string
	BaseURL    string // test override
}

type DiscogsClient struct {
	token      string
	maxRetries int
	baseURL    string
	client     *http.Client
	userAgent  string
}

type DiscogsEnrichment struct {
	Title       string
	Artist      string
	Album       string
	Label       string
	Released    string
	TrackNumber string
	DiscogsURL  string
	Score       int
	CoverImage  string
}

type discogsSearchResponse struct {
	Results []discogsSearchItem `json:"results"`
}

// discogsYear accepts JSON numbers or quoted numeric strings; Discogs search
// responses occasionally use strings for year.
type discogsYear int

func (y *discogsYear) UnmarshalJSON(data []byte) error {
	*y = 0
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("discogs year %q: %w", s, err)
		}
		*y = discogsYear(v)
		return nil
	}
	var v int
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*y = discogsYear(v)
	return nil
}

type discogsSearchItem struct {
	Title       string      `json:"title"`
	Year        discogsYear `json:"year"`
	Country     string      `json:"country"`
	Genre       []string    `json:"genre"`
	Style       []string    `json:"style"`
	Label       []string    `json:"label"`
	ResourceURL string      `json:"resource_url"`
	Format      []string    `json:"format"`
	CoverImage  string      `json:"cover_image"`
}

func NewDiscogsClient(cfg DiscogsClientConfig) *DiscogsClient {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	retries := cfg.MaxRetries
	if retries < 1 {
		retries = 2
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = discogsDefaultBaseURL
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = "OceanoPlayer/1.0 (+https://github.com/alemser/oceano-player)"
	}
	return &DiscogsClient{
		token:      token,
		maxRetries: retries,
		baseURL:    strings.TrimRight(baseURL, "/"),
		client:     &http.Client{Timeout: timeout},
		userAgent:  userAgent,
	}
}

func (c *DiscogsClient) EnrichTrack(ctx context.Context, artist, title, album, physicalFormat string) (*DiscogsEnrichment, error) {
	if c == nil {
		return nil, nil
	}
	artist = strings.TrimSpace(artist)
	title = strings.TrimSpace(title)
	album = strings.TrimSpace(album)
	if artist == "" || title == "" {
		return nil, nil
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		resp, err := c.search(ctx, artist, title)
		if err == nil {
			best := pickBestDiscogsResult(resp.Results, artist, title, album, physicalFormat)
			if best != nil && strings.TrimSpace(best.DiscogsURL) != "" {
				if pos, errPos := c.resolveTrackNumberFromRelease(ctx, best.DiscogsURL, title); errPos == nil && pos != "" {
					best.TrackNumber = pos
				}
			}
			return best, nil
		}
		lastErr = err
		if err == ErrRateLimit {
			return nil, err
		}
		if attempt == c.maxRetries-1 {
			break
		}
		jitter := time.Duration(150+rand.Intn(250)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jitter):
		}
	}
	return nil, lastErr
}

type discogsReleaseDoc struct {
	Tracklist []discogsTracklistItem `json:"tracklist"`
}

type discogsTracklistItem struct {
	Position string `json:"position"`
	Title    string `json:"title"`
	Type_    string `json:"type_"`
}

var featParenSuffix = regexp.MustCompile(`(?i)\s*\(\s*feat\.[^)]*\)\s*$`)

// Discogs `tracklist[].position` patterns vary (e.g. "1", "A2", "a2", "2A", "3D", "CD1-3").
// These regexes match simple single-side / single-index forms only.
var (
	canonicalPosLetterDigit = regexp.MustCompile(`(?i)^([a-z])[-.]?(\d{1,3})$`)
	canonicalPosDigitLetter = regexp.MustCompile(`(?i)^(\d{1,3})[-.]?([a-z])$`)
	canonicalPosDigitsOnly  = regexp.MustCompile(`^\d{1,3}$`)
)

// CanonicalDiscogsTrackPosition normalizes Discogs release tracklist `position` strings (and library
// PATCH `track_number` from oceano-web) for stable JSON/state and alignment with the Now Playing
// vinyl parser (`parseVinylTrackRef` in helpers.js).
//
// Contract:
//   - Pure numeric CD positions ("1", "12"): unchanged.
//   - Letter then digits, vinyl-style ("A2", "a2", "B-3", "c.4"): canonical "Side+track" with
//     uppercase letter ("A2", "B3", "C4").
//   - Digits then letter ("2A", "3d", "12-A"): canonical digit(s) + uppercase side letter ("2A", "3D").
//   - Multi-disc / Discogs compound labels ("CD1-3", "1-11", paths with "/"): only trim/collapse
//     whitespace; no structural rewrite.
func CanonicalDiscogsTrackPosition(pos string) string {
	pos = strings.TrimSpace(pos)
	if pos == "" {
		return ""
	}
	u := strings.ToUpper(pos)
	if strings.HasPrefix(u, "CD") || strings.Contains(pos, "/") {
		return strings.Join(strings.Fields(pos), " ")
	}
	s := strings.ReplaceAll(pos, " ", "")
	if m := canonicalPosLetterDigit.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(m[1]) + m[2]
	}
	if m := canonicalPosDigitLetter.FindStringSubmatch(s); m != nil {
		return m[1] + strings.ToUpper(m[2])
	}
	if canonicalPosDigitsOnly.MatchString(s) {
		return s
	}
	return strings.Join(strings.Fields(pos), " ")
}

func (c *DiscogsClient) resolveTrackNumberFromRelease(ctx context.Context, resourceURL, wantTitle string) (string, error) {
	if c == nil || strings.TrimSpace(resourceURL) == "" || strings.TrimSpace(wantTitle) == "" {
		return "", nil
	}
	body, err := c.getReleaseJSON(ctx, strings.TrimSpace(resourceURL))
	if err != nil || len(body) == 0 {
		return "", err
	}
	var doc discogsReleaseDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("discogs: decode release: %w", err)
	}
	return matchDiscogsTracklistPosition(doc.Tracklist, wantTitle), nil
}

func (c *DiscogsClient) getReleaseJSON(ctx context.Context, resourceURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Discogs token="+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimit
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("discogs: release http %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(io.LimitReader(res.Body, 2<<20))
}

// matchDiscogsTracklistPosition returns the Discogs tracklist position (e.g. "3", "A2") for the
// first row whose title matches wantTitle, skipping headings and index-only rows.
func matchDiscogsTracklistPosition(tracklist []discogsTracklistItem, wantTitle string) string {
	want := normalizeDiscogsTrackTitle(wantTitle)
	if want == "" {
		return ""
	}
	for _, row := range tracklist {
		t := strings.TrimSpace(row.Type_)
		if t == "heading" {
			continue
		}
		if t != "" && t != "track" {
			continue
		}
		cand := normalizeDiscogsTrackTitle(row.Title)
		if cand == "" {
			continue
		}
		if cand == want {
			return CanonicalDiscogsTrackPosition(row.Position)
		}
	}
	// Second pass: substring match (compilation titles, subtle punctuation differences).
	for _, row := range tracklist {
		t := strings.TrimSpace(row.Type_)
		if t == "heading" {
			continue
		}
		if t != "" && t != "track" {
			continue
		}
		cand := normalizeDiscogsTrackTitle(row.Title)
		if cand == "" {
			continue
		}
		if strings.Contains(cand, want) || strings.Contains(want, cand) {
			return CanonicalDiscogsTrackPosition(row.Position)
		}
	}
	return ""
}

func normalizeDiscogsTrackTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = featParenSuffix.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func (c *DiscogsClient) search(ctx context.Context, artist, title string) (*discogsSearchResponse, error) {
	u, err := url.Parse(c.baseURL + "/database/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("type", "release")
	q.Set("artist", artist)
	q.Set("track", title)
	q.Set("per_page", "10")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Discogs token="+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, ErrRateLimit
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("discogs: search http %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload discogsSearchResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func pickBestDiscogsResult(results []discogsSearchItem, artist, title, album, physicalFormat string) *DiscogsEnrichment {
	best := DiscogsEnrichment{}
	best.Score = -1
	for _, r := range results {
		score := scoreDiscogsCandidate(r, artist, title, album, physicalFormat)
		if score <= best.Score {
			continue
		}
		best = DiscogsEnrichment{
			Title:      title,
			Artist:     artist,
			Album:      extractAlbumFromDiscogsTitle(r.Title),
			Label:      firstNonEmpty(r.Label...),
			Released:   yearToString(int(r.Year)),
			DiscogsURL: strings.TrimSpace(r.ResourceURL),
			CoverImage: strings.TrimSpace(r.CoverImage),
			Score:      score,
		}
	}
	if best.Score < 45 {
		return nil
	}
	return &best
}

func scoreDiscogsCandidate(candidate discogsSearchItem, artist, title, album, physicalFormat string) int {
	score := 0
	normCand := strings.ToLower(strings.TrimSpace(candidate.Title))
	normArtist := strings.ToLower(strings.TrimSpace(artist))
	normTitle := strings.ToLower(strings.TrimSpace(title))
	normAlbum := strings.ToLower(strings.TrimSpace(album))

	if strings.Contains(normCand, normArtist) {
		score += 30
	}
	if strings.Contains(normCand, normTitle) {
		score += 40
	}
	if normAlbum != "" && strings.Contains(normCand, normAlbum) {
		score += 15
	}
	formatNeedle := strings.ToLower(strings.TrimSpace(physicalFormat))
	if formatNeedle != "" {
		for _, f := range candidate.Format {
			if strings.Contains(strings.ToLower(f), formatNeedle) {
				score += 15
				break
			}
		}
	}
	if int(candidate.Year) > 0 {
		score += 3
	}
	return score
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			return s
		}
	}
	return ""
}

func yearToString(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

func extractAlbumFromDiscogsTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	parts := strings.SplitN(s, " - ", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
