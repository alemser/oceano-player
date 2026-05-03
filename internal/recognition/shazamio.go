package recognition

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// shazamioDaemonScript is a Python script that runs as a persistent daemon.
// It imports shazamio once, then reads WAV paths from stdin (one per line)
// and writes a JSON result to stdout for each path. This avoids the 1–3 s
// cold-start cost of spawning a fresh interpreter per recognition call.
const shazamioDaemonScript = `import sys, asyncio, json
from shazamio import Shazam

async def main():
    shazam = Shazam()
    loop = asyncio.get_running_loop()
    while True:
        line = await loop.run_in_executor(None, sys.stdin.readline)
        if not line:
            break
        wav_path = line.strip()
        if not wav_path:
            continue
        try:
            result = await shazam.recognize(wav_path)
        except Exception as e:
            sys.stdout.write(json.dumps({'error': str(e)}) + '\n')
            sys.stdout.flush()
            continue
        if 'track' not in result:
            sys.stdout.write(json.dumps({}) + '\n')
            sys.stdout.flush()
            continue
        track = result['track']
        shazam_id = str(track.get('key', '') or '')
        album = ''
        for section in track.get('sections', []):
            if section.get('type') == 'SONG':
                for meta in section.get('metadata', []):
                    if meta.get('title') == 'Album':
                        album = meta.get('text', '')
        score = 0
        duration_ms = 0
        matches = result.get('matches', [])
        if matches:
            try:
                score = int(round(float(matches[0].get('score', 0) or 0)))
            except (ValueError, TypeError):
                score = 0
            try:
                duration_ms = int(matches[0].get('length', 0) or 0)
            except (ValueError, TypeError):
                duration_ms = 0
        sys.stdout.write(json.dumps({
            'shazam_id': shazam_id,
            'title': track.get('title', ''),
            'artist': track.get('subtitle', ''),
            'album': album,
            'score': score,
            'duration_ms': duration_ms,
        }) + '\n')
        sys.stdout.flush()

asyncio.run(main())
`

// ShazamioRecognizer identifies tracks via a persistent Python daemon that keeps
// shazamio loaded between calls. The daemon is started once and restarted
// automatically if it dies.
type ShazamioRecognizer struct {
	pythonBin  string
	scriptPath string

	mu     sync.Mutex
	proc   *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

// NewShazamioRecognizer creates a Shazamio (community shazamio client) recognizer
// backed by a persistent Python daemon. It returns (nil, error) with a specific reason if:
//   - the Python binary is missing
//   - shazamio is not importable
//   - the daemon script cannot be written or the daemon fails to start
func NewShazamioRecognizer(pythonBin string) (*ShazamioRecognizer, error) {
	if _, err := os.Stat(pythonBin); err != nil {
		return nil, fmt.Errorf("python binary not found at %s: %w", pythonBin, err)
	}
	if err := exec.Command(pythonBin, "-c", "import shazamio").Run(); err != nil {
		return nil, fmt.Errorf("shazamio not importable (python=%s): %w", pythonBin, err)
	}

	f, err := os.CreateTemp("", "shazamio-daemon-*.py")
	if err != nil {
		return nil, fmt.Errorf("create daemon script: %w", err)
	}
	if _, err := f.WriteString(shazamioDaemonScript); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("write daemon script: %w", err)
	}
	f.Close()

	s := &ShazamioRecognizer{
		pythonBin:  pythonBin,
		scriptPath: f.Name(),
	}
	if err := s.startProcess(); err != nil {
		os.Remove(f.Name())
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	return s, nil
}

func (s *ShazamioRecognizer) Name() string { return "Shazamio" }

// Recognize sends wavPath to the persistent daemon and returns the result.
// It enforces a hard 45 s subprocess timeout independent of ctx, and
// attempts a one-shot daemon restart if the process has died.
func (s *ShazamioRecognizer) Recognize(ctx context.Context, wavPath string) (*Result, error) {
	if strings.ContainsAny(wavPath, "\r\n") {
		return nil, fmt.Errorf("shazamio: wavPath contains newline characters")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Lazy start / restart after previous failure.
	if s.proc == nil {
		if err := s.startProcess(); err != nil {
			return nil, fmt.Errorf("shazamio: daemon restart: %w", err)
		}
	}

	if _, err := fmt.Fprintln(s.stdin, wavPath); err != nil {
		s.stopProcess()
		// One-shot restart: try to recover so the next call succeeds.
		if restartErr := s.startProcess(); restartErr != nil {
			return nil, fmt.Errorf("shazamio: write request (daemon restart also failed: %v): %w", restartErr, err)
		}
		return nil, fmt.Errorf("shazamio: write request (daemon restarted, retry next call): %w", err)
	}

	type readResult struct {
		line string
		err  error
	}
	// Capture the reader before spawning the goroutine so stopProcess() cannot
	// nil it out from under the goroutine mid-read.
	stdout := s.stdout
	ch := make(chan readResult, 1)
	go func() {
		line, err := stdout.ReadString('\n')
		ch <- readResult{line, err}
	}()

	const hardTimeout = 45 * time.Second
	select {
	case r := <-ch:
		if r.err != nil {
			s.stopProcess()
			return nil, fmt.Errorf("shazamio: read response: %w", r.err)
		}
		return parseShazamioJSONOutput([]byte(strings.TrimSpace(r.line)))
	case <-ctx.Done():
		s.stopProcess() // kills the process, unblocking the goroutine's read
		<-ch            // wait for the goroutine to exit
		return nil, ctx.Err()
	case <-time.After(hardTimeout):
		s.stopProcess()
		<-ch
		return nil, fmt.Errorf("shazamio: subprocess timeout after %s", hardTimeout)
	}
}

// Close stops the background Python daemon and removes the temp script file.
func (s *ShazamioRecognizer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopProcess()
	if s.scriptPath != "" {
		os.Remove(s.scriptPath)
		s.scriptPath = ""
	}
}

// startProcess launches the Python daemon. Must be called with s.mu held.
func (s *ShazamioRecognizer) startProcess() error {
	cmd := exec.Command(s.pythonBin, s.scriptPath)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdoutPipe.Close()
		return fmt.Errorf("start: %w", err)
	}
	s.proc = cmd
	s.stdin = stdin
	s.stdout = bufio.NewReader(stdoutPipe)
	return nil
}

// stopProcess kills the daemon and resets all process handles.
// Must be called with s.mu held.
func (s *ShazamioRecognizer) stopProcess() {
	if s.proc != nil && s.proc.Process != nil {
		s.proc.Process.Kill()
		s.proc.Wait()
	}
	if s.stdin != nil {
		s.stdin.Close()
	}
	s.proc = nil
	s.stdin = nil
	s.stdout = nil
}

// parseShazamioJSONOutput decodes one daemon stdout line. Wire format still uses
// shazam_id (upstream shazamio / Apple Music key field name).
func parseShazamioJSONOutput(data []byte) (*Result, error) {
	var payload struct {
		ShazamID   string `json:"shazam_id"`
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Album      string `json:"album"`
		Score      int    `json:"score"`
		DurationMs int    `json:"duration_ms"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("shazamio: parse output %q: %w", data, err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("shazamio: api error: %s", payload.Error)
	}
	if payload.Title == "" && payload.Artist == "" {
		return nil, nil
	}
	return &Result{
		ShazamID:    payload.ShazamID,
		Title:       payload.Title,
		Artist:      payload.Artist,
		Album:       payload.Album,
		Score:       payload.Score,
		DurationMs:  payload.DurationMs,
		MatchSource: "shazam",
	}, nil
}
