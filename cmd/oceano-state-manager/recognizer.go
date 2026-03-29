package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ErrRateLimit is returned by a Recognizer when the provider signals that the
// request quota has been exceeded. The caller should back off before retrying
// or fall through to a fallback provider.
var ErrRateLimit = errors.New("recognition: rate limit exceeded")

// RecognitionResult holds the identified track metadata.
type RecognitionResult struct {
	Title    string
	Artist   string
	Album    string
	Label    string
	Released string
	Score    int
}

// Recognizer identifies a track from a WAV audio file.
// Implementations must be safe for concurrent use.
type Recognizer interface {
	// Name returns the provider name used in log messages.
	Name() string

	// Recognize queries the provider with the given WAV file path.
	// Returns (nil, nil) when the track was not found (no match).
	// Returns (nil, ErrRateLimit) when quota is exceeded.
	// Returns (nil, err) on any other transport or API error.
	Recognize(ctx context.Context, wavPath string) (*RecognitionResult, error)
}

// captureFromPCMSocket reads duration of raw PCM from the source detector's
// PCM relay socket and writes a temporary WAV file (S16_LE, stereo, 44100 Hz).
// The caller must delete the file. This is the preferred capture method: it
// reads from audio already captured by oceano-source-detector without opening
// the ALSA device a second time.
func captureFromPCMSocket(ctx context.Context, socketPath string, duration time.Duration, dir string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("pcm socket: %w", err)
	}
	defer conn.Close()

	const (
		sampleRate = 44100
		channels   = 2
		bytesPerSample = 2 // S16_LE
	)
	totalBytes := int(duration.Seconds()) * sampleRate * channels * bytesPerSample

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(duration + 5*time.Second)
	}
	conn.SetDeadline(deadline)

	pcmData := make([]byte, totalBytes)
	if _, err := readFull(conn, pcmData); err != nil {
		return "", fmt.Errorf("pcm socket read: %w", err)
	}

	path := filepath.Join(dir, fmt.Sprintf("oceano-rec-%d.wav", time.Now().UnixNano()))
	if err := writePCMAsWAV(pcmData, sampleRate, channels, path); err != nil {
		return "", err
	}
	return path, nil
}

// writePCMAsWAV writes raw S16_LE PCM data as a WAV file.
func writePCMAsWAV(pcm []byte, sampleRate, channels int, path string) error {
	const bitsPerSample = 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	var hdr bytes.Buffer
	hdr.WriteString("RIFF")
	binary.Write(&hdr, binary.LittleEndian, uint32(36+len(pcm)))
	hdr.WriteString("WAVEfmt ")
	binary.Write(&hdr, binary.LittleEndian, uint32(16))              // PCM chunk size
	binary.Write(&hdr, binary.LittleEndian, uint16(1))               // PCM format
	binary.Write(&hdr, binary.LittleEndian, uint16(channels))
	binary.Write(&hdr, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&hdr, binary.LittleEndian, uint32(byteRate))
	binary.Write(&hdr, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&hdr, binary.LittleEndian, uint16(bitsPerSample))
	hdr.WriteString("data")
	binary.Write(&hdr, binary.LittleEndian, uint32(len(pcm)))

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(hdr.Bytes()); err != nil {
		return err
	}
	_, err = f.Write(pcm)
	return err
}

// readFull reads exactly len(buf) bytes from conn, respecting context cancellation.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
