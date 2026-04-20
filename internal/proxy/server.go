package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/xiaoqianghan/cc-insights/internal/config"
	"github.com/xiaoqianghan/cc-insights/internal/otel"
	"github.com/xiaoqianghan/cc-insights/internal/storage"
)

// Server is the HTTP proxy that receives OTEL metrics from Claude Code,
// stores them locally, and optionally forwards them upstream.
type Server struct {
	cfg       *config.Config
	db        *storage.DB
	srv       *http.Server
	client    *http.Client
	idleTimer *time.Timer
	mu        sync.Mutex
	wg        sync.WaitGroup // tracks in-flight background work (upstream forwarding)
}

// New creates a new proxy server with the given config and database.
func New(cfg *config.Config, db *storage.DB) *Server {
	s := &Server{
		cfg: cfg,
		db:  db,
		client: &http.Client{
			Timeout: cfg.Upstream.Timeout.Duration,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/health", s.handleHealth)

	s.srv = &http.Server{
		Addr:    cfg.Proxy.Listen,
		Handler: mux,
	}

	return s
}

// Start starts the HTTP server. It blocks until the server is shut down.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Proxy.Listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.cfg.Proxy.Listen, err)
	}
	log.Printf("proxy listening on %s", s.cfg.Proxy.Listen)
	err = s.srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the server and waits for in-flight work to drain.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.mu.Unlock()

	// Stop accepting new connections.
	err := s.srv.Shutdown(ctx)

	// Wait for in-flight upstream forwards to complete.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	return err
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Reset idle timer on every request.
	s.resetIdleTimer()

	// Parse and store metrics synchronously before acknowledging.
	// This ensures data is durable before the client considers it delivered.
	records, err := otel.Parse(body)
	if err != nil {
		log.Printf("otel parse error: %v", err)
		w.WriteHeader(http.StatusOK) // non-metric payload, still ACK
		// Still forward upstream so no data is silently lost.
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.forwardUpstream(body)
		}()
		return
	}
	if err := s.db.InsertMetrics(records); err != nil {
		log.Printf("insert metrics error: %v", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)

	// Forward to upstream in the background, tracked by WaitGroup for drain.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.forwardUpstream(body)
	}()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
