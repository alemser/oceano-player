package main

import (
	"context"
	"encoding/binary"
	"log"
	"math"
	"net"
	"os"
	"sync"
)

// --- PCM hub: fan-out raw PCM chunks to all connected socket clients ---
// Consumers receive continuous S16_LE stereo bytes at cfg.SampleRate Hz.
// This allows multiple readers (e.g. the recognizer) without opening the
// ALSA device a second time.

type pcmHub struct {
	mu       sync.Mutex
	clients  map[chan []byte]struct{}
	publish_ chan []byte
}

func newPCMHub() *pcmHub {
	return &pcmHub{
		clients:  make(map[chan []byte]struct{}),
		publish_: make(chan []byte, 4),
	}
}

func (h *pcmHub) publish(chunk []byte) {
	select {
	case h.publish_ <- chunk:
	default:
		// No consumer keeping up — drop chunk rather than block the audio loop.
	}
}

func (h *pcmHub) subscribe() chan []byte {
	ch := make(chan []byte, 32) // larger buffer: PCM chunks are ~8 KB each
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *pcmHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *pcmHub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-h.publish_:
			h.mu.Lock()
			for ch := range h.clients {
				select {
				case ch <- chunk:
				default:
					// Slow client — drop chunk.
				}
			}
			h.mu.Unlock()
		}
	}
}

// listenPCM accepts connections on a Unix socket and streams raw PCM bytes.
// The stream format is S16_LE, 2 channels, at cfg.SampleRate Hz (default 44100).
// There is no framing — bytes arrive as a continuous stream.
func listenPCM(ctx context.Context, socketPath string, hub *pcmHub) {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("PCM socket: failed to listen on %s: %v", socketPath, err)
		return
	}
	defer func() {
		ln.Close()
		_ = os.Remove(socketPath)
	}()
	log.Printf("PCM socket listening on %s", socketPath)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("PCM socket: accept error: %v", err)
			return
		}
		go handlePCMConn(ctx, conn, hub)
	}
}

func handlePCMConn(ctx context.Context, conn net.Conn, hub *pcmHub) {
	defer conn.Close()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	for {
		select {
		case <-ctx.Done():
			return
		case chunk := <-ch:
			if _, err := conn.Write(chunk); err != nil {
				return // client disconnected
			}
		}
	}
}

// --- VU hub: fan-out to all connected socket clients ---

type vuHub struct {
	mu       sync.Mutex
	clients  map[chan VUFrame]struct{}
	publish_ chan VUFrame
}

func newVUHub() *vuHub {
	return &vuHub{
		clients:  make(map[chan VUFrame]struct{}),
		publish_: make(chan VUFrame, 4),
	}
}

func (h *vuHub) publish(f VUFrame) {
	select {
	case h.publish_ <- f:
	default:
		// No consumer keeping up — drop frame rather than block the audio loop.
	}
}

func (h *vuHub) subscribe() chan VUFrame {
	ch := make(chan VUFrame, 8)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *vuHub) unsubscribe(ch chan VUFrame) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *vuHub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-h.publish_:
			h.mu.Lock()
			for ch := range h.clients {
				select {
				case ch <- frame:
				default:
					// Slow client — drop frame.
				}
			}
			h.mu.Unlock()
		}
	}
}

// listenVU accepts connections on a Unix socket and streams VU frames.
// Each frame is 8 bytes: float32 left RMS + float32 right RMS, little-endian.
func listenVU(ctx context.Context, socketPath string, hub *vuHub) {
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Printf("VU socket: failed to listen on %s: %v", socketPath, err)
		return
	}
	defer func() {
		ln.Close()
		_ = os.Remove(socketPath)
	}()
	log.Printf("VU socket listening on %s", socketPath)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("VU socket: accept error: %v", err)
			return
		}
		go handleVUConn(ctx, conn, hub)
	}
}

func handleVUConn(ctx context.Context, conn net.Conn, hub *vuHub) {
	defer conn.Close()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)

	buf := make([]byte, 8)
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-ch:
			binary.LittleEndian.PutUint32(buf[0:4], math.Float32bits(frame.Left))
			binary.LittleEndian.PutUint32(buf[4:8], math.Float32bits(frame.Right))
			if _, err := conn.Write(buf); err != nil {
				return // client disconnected
			}
		}
	}
}
