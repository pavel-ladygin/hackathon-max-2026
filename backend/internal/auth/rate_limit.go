package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
)

// limitBootstrap enforces the canonical 20 requests/minute/IP per process.
// Only the connection peer is trusted; proxy headers need deployment-level trust configuration.
func (s *Service) limitBootstrap(next http.HandlerFunc) http.HandlerFunc {
	type bucket struct {
		count int
		until time.Time
	}
	var mu sync.Mutex
	buckets := make(map[string]bucket)
	var cleanupAt time.Time
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
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
