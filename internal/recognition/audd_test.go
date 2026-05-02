package recognition

import (
	"errors"
	"testing"
)

func TestParseAudDResponse_NoMatch(t *testing.T) {
	body := `{"status":"success","result":null}`
	res, err := parseAudDResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
}

func TestParseAudDResponse_Success(t *testing.T) {
	body := `{"status":"success","result":{"artist":"A","title":"T","album":"Alb","release_date":"2020","label":"L","timecode":"0:12","song_link":"https://lis.tn/x"}}`
	res, err := parseAudDResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if res.Artist != "A" || res.Title != "T" || res.Album != "Alb" || res.MatchSource != "audd" {
		t.Fatalf("unexpected mapping: %+v", res)
	}
}

func TestParseAudDResponse_MusicBrainzArray(t *testing.T) {
	body := `{"status":"success","result":{"artist":"A","title":"T","album":"","release_date":"","label":"","timecode":"","song_link":"","musicbrainz":[{"isrc":"USXX12345678"}]}}`
	res, err := parseAudDResponse([]byte(body), 200)
	if err != nil {
		t.Fatal(err)
	}
	if res.ISRC != "USXX12345678" {
		t.Fatalf("ISRC: got %q", res.ISRC)
	}
}

func TestParseAudDResponse_ErrorRateLimit(t *testing.T) {
	body := `{"status":"error","error":{"code":901,"error_message":"limit"}}`
	_, err := parseAudDResponse([]byte(body), 200)
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected ErrRateLimit, got %v", err)
	}
}

func TestParseAudDResponse_ErrorOther(t *testing.T) {
	body := `{"status":"error","error":{"code":900,"error_message":"bad token"}}`
	_, err := parseAudDResponse([]byte(body), 200)
	if err == nil || errors.Is(err, ErrRateLimit) {
		t.Fatalf("expected non-rate-limit error, got %v", err)
	}
}
