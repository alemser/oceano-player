package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

type airplayDACPServiceResolver struct {
	mu    sync.Mutex
	cache map[string]airplayDACPServiceCacheEntry
}

type airplayTransportResolver interface {
	Resolve(ctx context.Context, dacpID, fallbackIP string) (string, int, string, error)
	WarmUp(dacpID, fallbackIP string)
}

type airplayDACPServiceCacheEntry struct {
	host      string
	port      int
	expiresAt time.Time
}

func newAirplayDACPServiceResolver() *airplayDACPServiceResolver {
	return &airplayDACPServiceResolver{
		cache: map[string]airplayDACPServiceCacheEntry{},
	}
}

func (r *airplayDACPServiceResolver) Resolve(ctx context.Context, dacpID, fallbackIP string) (string, int, string, error) {
	dacpID = strings.TrimSpace(strings.ToLower(dacpID))
	if dacpID == "" {
		return "", 0, "", errors.New("missing dacp id")
	}
	if host, port, ok := r.getCache(dacpID); ok {
		return host, port, "cache", nil
	}

	browseCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return "", 0, "", err
	}

	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := resolver.Browse(browseCtx, "_dacp._tcp", "local.", entries); err != nil {
		return "", 0, "", err
	}
	for {
		select {
		case <-browseCtx.Done():
			if host := strings.TrimSpace(fallbackIP); host != "" {
				return host, 3689, "fallback", nil
			}
			return "", 0, "", browseCtx.Err()
		case entry := <-entries:
			if entry == nil {
				continue
			}
			if !strings.Contains(strings.ToLower(entry.Instance), dacpID) {
				continue
			}
			host := chooseDACPHost(entry, fallbackIP)
			if host == "" {
				continue
			}
			port := entry.Port
			if port <= 0 {
				port = 3689
			}
			r.setCache(dacpID, host, port)
			return host, port, "mdns", nil
		}
	}
}

func (r *airplayDACPServiceResolver) getCache(dacpID string) (string, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[dacpID]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(r.cache, dacpID)
		return "", 0, false
	}
	return entry.host, entry.port, true
}

func (r *airplayDACPServiceResolver) setCache(dacpID, host string, port int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[dacpID] = airplayDACPServiceCacheEntry{
		host:      host,
		port:      port,
		expiresAt: time.Now().Add(90 * time.Second),
	}
}

// chooseDACPHost always prefers IPv4 — DACP on modern iOS only accepts
// connections on IPv4 regardless of the client IP family reported by shairport.
func chooseDACPHost(entry *zeroconf.ServiceEntry, _ string) string {
	if len(entry.AddrIPv4) > 0 {
		return entry.AddrIPv4[0].String()
	}
	if len(entry.AddrIPv6) > 0 {
		return entry.AddrIPv6[0].String()
	}
	host := strings.TrimSuffix(strings.TrimSpace(entry.HostName), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return ""
}

// WarmUp runs a background mDNS browse so the cache is hot before a command
// arrives. Called from the capabilities endpoint which iOS polls every ~4s.
func (r *airplayDACPServiceResolver) WarmUp(dacpID, fallbackIP string) {
	dacpID = strings.TrimSpace(strings.ToLower(dacpID))
	if dacpID == "" {
		return
	}
	if _, _, ok := r.getCache(dacpID); ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return
	}
	entries := make(chan *zeroconf.ServiceEntry, 32)
	if err := resolver.Browse(ctx, "_dacp._tcp", "local.", entries); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-entries:
			if entry == nil {
				continue
			}
			if !strings.Contains(strings.ToLower(entry.Instance), dacpID) {
				continue
			}
			host := chooseDACPHost(entry, fallbackIP)
			if host == "" {
				continue
			}
			port := entry.Port
			if port <= 0 {
				port = 3689
			}
			r.setCache(dacpID, host, port)
			return
		}
	}
}
