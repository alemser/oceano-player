package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
)

const shazamPythonScript = `
import sys, asyncio, json
from shazamio import Shazam

async def identify():
    shazam = Shazam()
    try:
        result = await shazam.recognize(sys.argv[1])
    except Exception as e:
        print(json.dumps({"error": str(e)}))
        return
    if 'track' not in result:
        print(json.dumps({}))
        return
    track = result['track']
    album = ''
    for section in track.get('sections', []):
        if section.get('type') == 'SONG':
            for meta in section.get('metadata', []):
                if meta.get('title') == 'Album':
                    album = meta.get('text', '')
    print(json.dumps({
        'title':  track.get('title', ''),
        'artist': track.get('subtitle', ''),
        'album':  album,
    }))

asyncio.run(identify())
`

// ShazamRecognizer implements Recognizer by shelling out to shazamio
// (a Python library that calls the Shazam API). It requires a Python
// virtualenv at pythonBin with shazamio installed.
type ShazamRecognizer struct {
	pythonBin string // e.g. /opt/shazam-env/bin/python
}

// NewShazamRecognizer returns a ShazamRecognizer if pythonBin exists and
// has shazamio installed, or nil if the prerequisites are not met.
func NewShazamRecognizer(pythonBin string) *ShazamRecognizer {
	if _, err := os.Stat(pythonBin); err != nil {
		return nil
	}
	// Quick check: import shazamio
	if err := exec.Command(pythonBin, "-c", "import shazamio").Run(); err != nil {
		return nil
	}
	return &ShazamRecognizer{pythonBin: pythonBin}
}

func (s *ShazamRecognizer) Name() string { return "Shazam" }

func (s *ShazamRecognizer) Recognize(ctx context.Context, wavPath string) (*RecognitionResult, error) {
	// Write the inline Python script to a temp file.
	pyFile, err := os.CreateTemp("", "shazam-*.py")
	if err != nil {
		return nil, fmt.Errorf("shazam: create temp script: %w", err)
	}
	defer os.Remove(pyFile.Name())
	if _, err := pyFile.WriteString(shazamPythonScript); err != nil {
		pyFile.Close()
		return nil, fmt.Errorf("shazam: write script: %w", err)
	}
	pyFile.Close()

	out, err := exec.CommandContext(ctx, s.pythonBin, pyFile.Name(), wavPath).Output()
	if err != nil {
		return nil, fmt.Errorf("shazam: python: %w", err)
	}

	var payload struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("shazam: parse output: %w", err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("shazam: api error: %s", payload.Error)
	}
	if payload.Title == "" && payload.Artist == "" {
		return nil, nil // no match
	}
	return &RecognitionResult{
		Title:  payload.Title,
		Artist: payload.Artist,
		Album:  payload.Album,
	}, nil
}

// ChainRecognizer tries each Recognizer in order and returns the first
// non-nil result. On rate limit or error from one provider it moves to
// the next instead of giving up. Returns (nil, nil) only when all
// providers report no match.
type ChainRecognizer struct {
	chain []Recognizer
}

// NewChainRecognizer returns a ChainRecognizer over the given recognizers,
// skipping any nil entries (e.g. when Shazam prerequisites are absent).
func NewChainRecognizer(recognizers ...Recognizer) Recognizer {
	var chain []Recognizer
	for _, r := range recognizers {
		if r != nil {
			chain = append(chain, r)
		}
	}
	switch len(chain) {
	case 0:
		return nil
	case 1:
		return chain[0]
	}
	return &ChainRecognizer{chain: chain}
}

func (c *ChainRecognizer) Name() string {
	names := make([]string, len(c.chain))
	for i, r := range c.chain {
		names[i] = r.Name()
	}
	result := names[0]
	for _, n := range names[1:] {
		result += "→" + n
	}
	return result
}

func (c *ChainRecognizer) Recognize(ctx context.Context, wavPath string) (*RecognitionResult, error) {
	var lastErr error
	for i, r := range c.chain {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		result, err := r.Recognize(ctx, wavPath)
		if err != nil {
			log.Printf("recognizer chain: %s: %v — trying next", r.Name(), err)
			lastErr = err
			continue
		}
		if result != nil {
			if i == 0 {
				log.Printf("recognizer chain: %s: match %s — %s", r.Name(), result.Artist, result.Title)
			} else {
				log.Printf("recognizer chain: %s: fallback match %s — %s", r.Name(), result.Artist, result.Title)
			}
			return result, nil
		}
		log.Printf("recognizer chain: %s: no match — trying next", r.Name())
	}
	// All providers tried: return last error if any, else no-match.
	return nil, lastErr
}
