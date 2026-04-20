package proxy

import (
	"bytes"
	"io"
	"log"
	"net/http"
)

// forwardUpstream sends the raw OTEL payload to the configured upstream URL.
// If no upstream URL is configured, this is a no-op (local-only mode).
func (s *Server) forwardUpstream(body []byte) {
	if s.cfg.Upstream.URL == "" {
		return
	}

	req, err := http.NewRequest(http.MethodPost, s.cfg.Upstream.URL, bytes.NewReader(body))
	if err != nil {
		log.Printf("upstream: failed to create request: %v", err)
		s.db.IncrementStat("upstream_failures")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.cfg.Upstream.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		log.Printf("upstream: request failed: %v", err)
		s.db.IncrementStat("upstream_failures")
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("upstream: unexpected status %d", resp.StatusCode)
		s.db.IncrementStat("upstream_failures")
	}
}
