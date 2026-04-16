package recognition

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ACRCloudConfig holds credentials for the ACRCloud audio fingerprinting API.
type ACRCloudConfig struct {
	Host      string
	AccessKey string
	SecretKey string
}

// ACRCloudRecognizer implements Recognizer using the ACRCloud HTTP API.
type ACRCloudRecognizer struct {
	cfg    ACRCloudConfig
	client *http.Client
}

func NewACRCloudRecognizer(cfg ACRCloudConfig) *ACRCloudRecognizer {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", addr)
		},
	}
	return &ACRCloudRecognizer{
		cfg:    cfg,
		client: &http.Client{Timeout: 25 * time.Second, Transport: transport},
	}
}

func (r *ACRCloudRecognizer) Name() string { return "ACRCloud" }

func (r *ACRCloudRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
	f, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().Unix()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := writeFields(mw, map[string]string{
		"access_key":        r.cfg.AccessKey,
		"data_type":         "audio",
		"signature_version": "1",
		"signature":         r.sign(timestamp),
		"timestamp":         strconv.FormatInt(timestamp, 10),
		"sample_bytes":      strconv.FormatInt(fi.Size(), 10),
	}); err != nil {
		return nil, err
	}
	fw, err := mw.CreateFormFile("sample", filepath.Base(wavPath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, err
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://%s/v1/identify", r.cfg.Host), &body)
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

	var result acrResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ACRCloud: decode response: %w", err)
	}

	switch result.Status.Code {
	case 0:
	case 1001:
		return nil, nil
	case 4001, 4003:
		return nil, ErrRateLimit
	default:
		return nil, fmt.Errorf("ACRCloud error %d: %s", result.Status.Code, result.Status.Msg)
	}

	if len(result.Metadata.Music) == 0 {
		return nil, nil
	}
	m := result.Metadata.Music[0]
	artist := ""
	if len(m.Artists) > 0 {
		artist = m.Artists[0].Name
	}
	return &Result{
		ACRID:      m.ACRID,
		ISRC:       m.ExternalIDs.ISRC,
		Title:      m.Title,
		Artist:     artist,
		Album:      m.Album.Name,
		Label:      m.Label,
		Released:   m.ReleaseDate,
		Score:      m.Score,
		DurationMs: m.DurationMs,
	}, nil
}

func (r *ACRCloudRecognizer) sign(timestamp int64) string {
	msg := fmt.Sprintf("POST\n/v1/identify\n%s\naudio\n1\n%d", r.cfg.AccessKey, timestamp)
	mac := hmac.New(sha1.New, []byte(r.cfg.SecretKey))
	mac.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func writeFields(mw *multipart.Writer, fields map[string]string) error {
	for k, v := range fields {
		fw, err := mw.CreateFormField(k)
		if err != nil {
			return err
		}
		if _, err := fw.Write([]byte(v)); err != nil {
			return err
		}
	}
	return nil
}

type acrResponse struct {
	Status struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	} `json:"status"`
	Metadata struct {
		Music []acrMusic `json:"music"`
	} `json:"metadata"`
}

type acrMusic struct {
	ACRID       string         `json:"acrid"`
	Title       string         `json:"title"`
	Artists     []acrArtist    `json:"artists"`
	Album       acrAlbum       `json:"album"`
	Label       string         `json:"label"`
	ReleaseDate string         `json:"release_date"`
	Score       int            `json:"score"`
	DurationMs  int            `json:"duration_ms"`
	ExternalIDs acrExternalIDs `json:"external_ids"`
}

type acrExternalIDs struct {
	ISRC string `json:"isrc"`
}

type acrArtist struct {
	Name string `json:"name"`
}

type acrAlbum struct {
	Name string `json:"name"`
}
