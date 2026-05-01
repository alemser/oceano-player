package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var airplayMetadataItemRE = regexp.MustCompile(
	`(?s)<item>\s*<type>([0-9a-fA-F]{8})</type>\s*<code>([0-9a-fA-F]{8})</code>\s*<length>\d+</length>\s*(?:<data encoding="base64">(.*?)</data>)?\s*</item>`,
)

type airplayDACPContext struct {
	ActiveRemote string
	DACPID       string
	ClientIP     string
	UpdatedAt    time.Time
}

func (c airplayDACPContext) ready(now time.Time) bool {
	if strings.TrimSpace(c.ActiveRemote) == "" || strings.TrimSpace(c.DACPID) == "" || strings.TrimSpace(c.ClientIP) == "" {
		return false
	}
	if c.UpdatedAt.IsZero() {
		return false
	}
	return now.Sub(c.UpdatedAt) <= 5*time.Minute
}

type airplayDACPContextReader interface {
	Snapshot() airplayDACPContext
}

type airplayDACPMonitor struct {
	pipePath string

	mu      sync.Mutex
	ctx     airplayDACPContext
	running bool
}

func newAirplayDACPMonitor(pipePath string) *airplayDACPMonitor {
	return &airplayDACPMonitor{pipePath: strings.TrimSpace(pipePath)}
}

func (m *airplayDACPMonitor) Start(ctx context.Context) {
	if m == nil || m.pipePath == "" {
		return
	}
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()
	go m.run(ctx)
}

func (m *airplayDACPMonitor) Snapshot() airplayDACPContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctx
}

func (m *airplayDACPMonitor) run(ctx context.Context) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 8192)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		type openResult struct {
			f   *os.File
			err error
		}
		openCh := make(chan openResult, 1)
		go func() {
			f, err := os.Open(m.pipePath)
			openCh <- openResult{f: f, err: err}
		}()

		var f *os.File
		select {
		case <-ctx.Done():
			return
		case r := <-openCh:
			if r.err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			f = r.f
		}

		for {
			select {
			case <-ctx.Done():
				_ = f.Close()
				return
			default:
			}
			n, err := f.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				buf = m.drain(buf)
			}
			if err != nil {
				_ = f.Close()
				break
			}
		}
	}
}

func (m *airplayDACPMonitor) drain(buf []byte) []byte {
	locs := airplayMetadataItemRE.FindAllSubmatchIndex(buf, -1)
	if len(locs) == 0 {
		if len(buf) > 262144 {
			return buf[len(buf)-8192:]
		}
		return buf
	}
	for _, loc := range locs {
		itemType := decodeTag(string(buf[loc[2]:loc[3]]))
		code := decodeTag(string(buf[loc[4]:loc[5]]))
		var rawData []byte
		if loc[6] >= 0 {
			b64 := strings.TrimSpace(string(buf[loc[6]:loc[7]]))
			if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
				rawData = decoded
			}
		}
		m.applyItem(itemType, code, strings.TrimSpace(string(rawData)))
	}
	return buf[locs[len(locs)-1][1]:]
}

func (m *airplayDACPMonitor) applyItem(itemType, code, strVal string) {
	switch itemType {
	case "ssnc":
		switch code {
		case "acre":
			if strVal == "" {
				return
			}
			m.mu.Lock()
			m.ctx.ActiveRemote = strVal
			m.ctx.UpdatedAt = time.Now()
			m.mu.Unlock()
		case "daid":
			if strVal == "" {
				return
			}
			m.mu.Lock()
			m.ctx.DACPID = strVal
			m.ctx.UpdatedAt = time.Now()
			m.mu.Unlock()
		case "pend", "pfls", "stop":
			m.mu.Lock()
			m.ctx = airplayDACPContext{}
			m.mu.Unlock()
		}
	case "core":
		// "clip" is the sender/client IP in shairport metadata.
		if code == "clip" {
			if strVal == "" {
				return
			}
			ip := normalizeClientIP(strVal)
			if ip == "" {
				return
			}
			m.mu.Lock()
			m.ctx.ClientIP = ip
			m.ctx.UpdatedAt = time.Now()
			m.mu.Unlock()
		}
	}
}

func normalizeClientIP(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "::ffff:") {
		s = strings.TrimPrefix(s, "::ffff:")
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return ""
}

func decodeTag(hexStr string) string {
	hexStr = strings.TrimSpace(hexStr)
	if hexStr == "" {
		return ""
	}
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return strings.ToLower(hexStr)
	}
	return strings.ToLower(string(b))
}
