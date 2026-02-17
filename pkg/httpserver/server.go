package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// Server is a thin wrapper around net/http that lets multiple components
// (channels, services) register webhook endpoints on a single listener.
type Server struct {
	addr string

	mu       sync.Mutex
	mux      *http.ServeMux
	srv      *http.Server
	listener net.Listener
	paths    map[string]struct{}
}

func New(addr string) *Server {
	return &Server{
		addr:  addr,
		mux:   http.NewServeMux(),
		paths: make(map[string]struct{}),
	}
}

// HandleFunc registers a handler for a path. It errors on duplicate registration.
func (s *Server) HandleFunc(path string, fn http.HandlerFunc) error {
	return s.Handle(path, fn)
}

// Handle registers a handler for a path. It errors on duplicate registration.
func (s *Server) Handle(path string, handler http.Handler) error {
	if path == "" {
		return fmt.Errorf("httpserver: empty path")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.paths[path]; exists {
		return fmt.Errorf("httpserver: handler already registered for path %q", path)
	}
	s.paths[path] = struct{}{}
	s.mux.Handle(path, handler)
	return nil
}

// Start begins listening in a background goroutine.
// It returns an error if the address cannot be bound.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.srv != nil {
		return nil
	}

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}

	s.listener = ln
	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
	}

	go func() {
		logger.InfoCF("http", "Gateway HTTP server started", map[string]interface{}{"addr": s.addr})
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("http", "Gateway HTTP server error", map[string]interface{}{"error": err.Error()})
		}
	}()

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.listener = nil
	s.mu.Unlock()

	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
