package recognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

const shazamPythonScript = "import sys, asyncio, json\n" +
	"from shazamio import Shazam\n\n" +
	"async def identify():\n" +
	"    shazam = Shazam()\n" +
	"    try:\n" +
	"        result = await shazam.recognize(sys.argv[1])\n" +
	"    except Exception as e:\n" +
	"        print(json.dumps({'error': str(e)}))\n" +
	"        return\n" +
	"    if 'track' not in result:\n" +
	"        print(json.dumps({}))\n" +
	"        return\n" +
	"    track = result['track']\n" +
	"    shazam_id = str(track.get('key', '') or '')\n" +
	"    album = ''\n" +
	"    for section in track.get('sections', []):\n" +
	"        if section.get('type') == 'SONG':\n" +
	"            for meta in section.get('metadata', []):\n" +
	"                if meta.get('title') == 'Album':\n" +
	"                    album = meta.get('text', '')\n" +
	"    print(json.dumps({\n" +
	"        'shazam_id': shazam_id,\n" +
	"        'title': track.get('title', ''),\n" +
	"        'artist': track.get('subtitle', ''),\n" +
	"        'album': album,\n" +
	"    }))\n\n" +
	"asyncio.run(identify())\n"

type ShazamRecognizer struct {
	pythonBin string
}

func NewShazamRecognizer(pythonBin string) *ShazamRecognizer {
	if _, err := os.Stat(pythonBin); err != nil {
		return nil
	}
	if err := exec.Command(pythonBin, "-c", "import shazamio").Run(); err != nil {
		return nil
	}
	return &ShazamRecognizer{pythonBin: pythonBin}
}

func (s *ShazamRecognizer) Name() string { return "Shazam" }

func (s *ShazamRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
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
		ShazamID string `json:"shazam_id"`
		Title    string `json:"title"`
		Artist   string `json:"artist"`
		Album    string `json:"album"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("shazam: parse output: %w", err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("shazam: api error: %s", payload.Error)
	}
	if payload.Title == "" && payload.Artist == "" {
		return nil, nil
	}
	return &Result{
		ShazamID: payload.ShazamID,
		Title:    payload.Title,
		Artist:   payload.Artist,
		Album:    payload.Album,
	}, nil
}
