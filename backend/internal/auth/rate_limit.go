package auth

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

// limitBootstrap enforces the canonical 20 requests/minute/IP per process.
func (s *Service) limitBootstrap(next http.HandlerFunc) http.HandlerFunc {
	type bucket struct {
		count int
		until time.Time
	}
	var mu sync.Mutex
	buckets := make(map[string]bucket)
	var cleanupAt time.Time
	return func(w http.ResponseWriter, r *http.Request) {
		ip := s.clientIP(r)
		now := s.now()
		mu.Lock()
		if !now.Before(cleanupAt) {
			for key, value := range buckets {
				if !now.Before(value.until) {
					delete(buckets, key)
				}
			}
			cleanupAt = now.Add(time.Minute)
		}
		b, exists := buckets[ip]
		retry := 0
		if !exists && len(buckets) >= 4096 {
			// Bound unauthenticated memory use, even under a large number of source IPs.
			retry = 60
		} else {
			if !now.Before(b.until) {
				b = bucket{until: now.Add(time.Minute)}
			}
			if b.count >= 20 {
				retry = int(b.until.Sub(now).Seconds()) + 1
			} else {
				b.count++
				buckets[ip] = b
			}
		}
		mu.Unlock()
		if retry > 0 {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Too many requests"})
			return
		}
		next(w, r)
	}
}

func (s *Service) clientIP(r *http.Request) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peer = host
	}
	peerIP := net.ParseIP(peer)
	if peerIP == nil || !s.isTrustedProxy(peerIP) {
		return peer
	}
	for _, value := range reverseForwardedFor(r.Header.Values("X-Forwarded-For")) {
		candidate := net.ParseIP(value)
		if candidate == nil {
			continue
		}
		if !s.isTrustedProxy(candidate) {
			return candidate.String()
		}
	}
	return peerIP.String()
}

func reverseForwardedFor(headers []string) []string {
	var values []string
	for _, header := range headers {
		for _, value := range strings.Split(header, ",") {
			value = strings.TrimSpace(value)
			if value != "" {
				values = append(values, value)
			}
		}
	}
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
	return values
}

func (s *Service) isTrustedProxy(ip net.IP) bool {
	for _, network := range s.trustedProxyCIDRs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
