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
			return pickBestDiscogsResult(resp.Results, artist, title, album, physicalFormat), nil
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

