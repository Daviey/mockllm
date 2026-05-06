package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Daviey/mockllm/internal/provider"
)

var Version = "dev"

type Server struct {
	registry *provider.Registry
	port     int
}

func New(registry *provider.Registry, port int) *Server {
	return &Server{
		registry: registry,
		port:     port,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	mux.HandleFunc("/_health", s.handleHealth)
	mux.HandleFunc("/_providers", s.handleProviders)
	mux.HandleFunc("/_reset", s.handleReset)
	mux.HandleFunc("/_version", s.handleVersion)

	handler := corsMiddleware(recoveryMiddleware(requestLogger(mux)))

	addr := fmt.Sprintf(":%d", s.port)
	httpServer := &http.Server{Addr: addr, Handler: handler}

	for _, p := range s.registry.Providers() {
		slog.Info("loaded provider", "name", p)
	}
	slog.Info("mockllm starting", "addr", addr, "version", Version)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("shutting down", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
		return err
	}

	slog.Info("server stopped")
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"providers": s.registry.Providers(),
	})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.registry.Reset()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	wantsStream := false
	if r.Method == "POST" {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var req map[string]interface{}
		if json.Unmarshal(body, &req) == nil {
			if val, ok := req["stream"].(bool); ok {
				wantsStream = val
			}
		}
	}

	p, resp, found := s.registry.Match(r.Method, r.URL.Path, wantsStream)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("no matching endpoint for %s %s", r.Method, r.URL.Path),
		})
		return
	}

	if resp == nil {
		writeJSON(w, http.StatusNoContent, nil)
		return
	}

	if resp.Delay != "" {
		if d, err := time.ParseDuration(resp.Delay); err == nil {
			time.Sleep(d)
		}
	}

	slog.Debug("response selected",
		"provider", p.Name,
		"status", resp.Status,
		"label", resp.Label,
		"stream", resp.Stream || len(resp.StreamChunks) > 0,
	)

	if resp.Stream || len(resp.StreamChunks) > 0 {
		s.handleStream(w, resp)
		return
	}

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.Status)
	w.Write(resp.Body)
}

func (s *Server) handleStream(w http.ResponseWriter, resp *provider.Response) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	flusher, canFlush := w.(http.Flusher)

	if len(resp.StreamChunks) > 0 {
		for _, chunk := range resp.StreamChunks {
			if chunk.Delay != "" {
				if d, err := time.ParseDuration(chunk.Delay); err == nil {
					time.Sleep(d)
				}
			}
			fmt.Fprintf(w, "data: %s\n\n", strings.TrimSpace(string(chunk.Data)))
			if canFlush {
				flusher.Flush()
			}
		}
	} else if resp.Body != nil {
		var body map[string]interface{}
		if err := json.Unmarshal(resp.Body, &body); err == nil {
			chunkDelay := resp.Delay
			if chunkDelay == "" {
				chunkDelay = "50ms"
			}
			if d, err := time.ParseDuration(chunkDelay); err == nil {
				time.Sleep(d)
			}
			data, _ := json.Marshal(body)
			fmt.Fprintf(w, "data: %s\n\n", strings.TrimSpace(string(data)))
			if canFlush {
				flusher.Flush()
			}
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.Encode(v)
	}
}

func Port(port string) int {
	if port == "" {
		return 8080
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 8080
	}
	return p
}
