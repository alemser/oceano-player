package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"time"

	internalrecognition "github.com/alemser/oceano-player/internal/recognition"
)

var ErrRateLimit = internalrecognition.ErrRateLimit

type RecognitionResult = internalrecognition.Result
type Recognizer = internalrecognition.Recognizer

// captureFromPCMSocket reads duration of raw PCM from the source detector's
// PCM relay socket and writes a temporary WAV file (S16_LE, stereo, 44100 Hz).
// The caller must delete the file. This is the preferred capture method: it
// reads from audio already captured by oceano-source-detector without opening
// the ALSA device a second time.
//
// skipDuration discards that many seconds of PCM before capturing. Use this
// after a track-boundary trigger to flush buffered audio from the previous
// track and reduce false positives caused by buffer latency.
func captureFromPCMSocket(ctx context.Context, socketPath string, duration, skipDuration time.Duration, dir string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("pcm socket: %w", err)
	}
	defer conn.Close()

	const (
		sampleRate     = 44100
		channels       = 2
		bytesPerSample = 2 // S16_LE
	)

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(skipDuration + duration + 5*time.Second)
	}
	conn.SetDeadline(deadline)

	// Discard buffered audio from the previous track before the real capture.
	if skipDuration > 0 {
		skipBytes := int(skipDuration.Seconds()) * sampleRate * channels * bytesPerSample
		skipBuf := make([]byte, skipBytes)
		if _, err := readFull(conn, skipBuf); err != nil {
			return "", fmt.Errorf("pcm socket skip: %w", err)
		}
	}

	totalBytes := int(duration.Seconds()) * sampleRate * channels * bytesPerSample
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
	binary.Write(&hdr, binary.LittleEndian, uint32(16)) // PCM chunk size
	binary.Write(&hdr, binary.LittleEndian, uint16(1))  // PCM format
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

// wavPCMLevelStats reads a PCM WAV (16-bit LE mono or stereo) and returns RMS
// (sqrt of mean sample energy) and peak magnitude in 0..1 for telemetry. Best-effort:
// returns an error only when the file cannot be read or has no usable data chunk.
func wavPCMLevelStats(path string) (meanRMS, peak float64, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	idx := bytes.Index(raw, []byte("data"))
	if idx < 0 || len(raw) < idx+8 {
		return 0, 0, fmt.Errorf("wav: missing data chunk")
	}
	chunkSize := int(binary.LittleEndian.Uint32(raw[idx+4 : idx+8]))
	pcmStart := idx + 8
	pcmEnd := pcmStart + chunkSize
	if pcmEnd > len(raw) {
		pcmEnd = len(raw)
	}
	n := pcmEnd - pcmStart
	if n < 2 || n%2 != 0 {
		return 0, 0, fmt.Errorf("wav: empty or odd pcm")
	}
	samples := n / 2
	var sumSq float64
	peak = 0
	for i := 0; i < samples; i++ {
		v := int16(binary.LittleEndian.Uint16(raw[pcmStart+i*2 : pcmStart+i*2+2]))
		x := float64(v) / 32768.0
		if ax := math.Abs(x); ax > peak {
			peak = ax
		}
		sumSq += x * x
	}
	return math.Sqrt(sumSq / float64(samples)), peak, nil
}
