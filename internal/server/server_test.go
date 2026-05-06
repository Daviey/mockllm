package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Daviey/mockllm/internal/provider"
)

func setupServer(t *testing.T) *Server {
	t.Helper()
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "/v1",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/chat",
				Method: "POST",
				Responses: []provider.Response{
					{Status: 200, Body: json.RawMessage(`{"ok":true}`), Label: "ok"},
				},
			},
			{
				Path:   "/chat/stream",
				Method: "POST",
				Responses: []provider.Response{
					{Status: 200, Body: json.RawMessage(`{"ok":true}`), Label: "ok"},
					{Status: 200, Stream: true, Label: "stream", StreamChunks: []provider.StreamChunk{
						{Data: json.RawMessage(`{"chunk":"hello"}`)},
						{Data: json.RawMessage(`{"chunk":"world"}`)},
					}},
				},
			},
			{
				Path:      "/errors",
				Method:    "POST",
				MatchMode: "sequential",
				Responses: []provider.Response{
					{Status: 200, Label: "ok", Body: json.RawMessage(`{"status":"ok"}`)},
					{Status: 429, Label: "rate_limit", Body: json.RawMessage(`{"error":"rate_limited"}`)},
					{Status: 500, Label: "error", Body: json.RawMessage(`{"error":"internal"}`)},
				},
			},
		},
	})
	return New(r, 0)
}

func TestHealthEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected ok, got %s", body["status"])
	}
}

func TestProvidersEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/_providers", nil)
	w := httptest.NewRecorder()
	srv.handleProviders(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestMatchingEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(`{"model":"gpt-4"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]bool
	json.NewDecoder(w.Body).Decode(&body)
	if !body["ok"] {
		t.Fatal("expected ok:true")
	}
}

func TestNotFoundEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/v1/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestStreamingEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("POST", "/v1/chat/stream", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, `{"chunk":"hello"}`) {
		t.Fatal("expected first chunk in SSE output")
	}
	if !strings.Contains(body, `{"chunk":"world"}`) {
		t.Fatal("expected second chunk in SSE output")
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatal("expected [DONE] in SSE output")
	}
}

func TestSequentialErrors(t *testing.T) {
	srv := setupServer(t)

	expectedStatuses := []int{200, 429, 500, 200}
	for i, want := range expectedStatuses {
		req := httptest.NewRequest("POST", "/v1/errors", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.handleRequest(w, req)

		if w.Code != want {
			t.Fatalf("call %d: expected %d, got %d", i, want, w.Code)
		}
	}
}

func TestResetEndpoint(t *testing.T) {
	srv := setupServer(t)

	req := httptest.NewRequest("POST", "/v1/errors", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	req = httptest.NewRequest("POST", "/v1/errors", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	srv.handleRequest(w, req)
	if w.Code != 429 {
		t.Fatalf("expected 429, got %d", w.Code)
	}

	resetReq := httptest.NewRequest("POST", "/_reset", nil)
	resetW := httptest.NewRecorder()
	srv.handleReset(resetW, resetReq)
	if resetW.Code != 200 {
		t.Fatalf("expected 200 on reset, got %d", resetW.Code)
	}

	req = httptest.NewRequest("POST", "/v1/errors", strings.NewReader(`{}`))
	w = httptest.NewRecorder()
	srv.handleRequest(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 after reset, got %d", w.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("OPTIONS", "/anything", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 204 {
		t.Fatalf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("expected *, got %s", origin)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	handler := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest("GET", "/crash", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestVersionEndpoint(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/_version", nil)
	w := httptest.NewRecorder()
	srv.handleVersion(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["version"] != Version {
		t.Fatalf("expected %s, got %s", Version, body["version"])
	}
}

func TestResponseHeaders(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/headers",
				Method: "GET",
				Responses: []provider.Response{
					{
						Status:  200,
						Body:    json.RawMessage(`{}`),
						Headers: map[string]string{"X-Custom": "test-value", "Retry-After": "5"},
					},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("GET", "/headers", nil)
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if v := w.Header().Get("X-Custom"); v != "test-value" {
		t.Fatalf("expected test-value, got %s", v)
	}
	if v := w.Header().Get("Retry-After"); v != "5" {
		t.Fatalf("expected 5, got %s", v)
	}
}

func TestPortHelper(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 8080},
		{"9090", 9090},
		{"abc", 8080},
		{"0", 0},
	}
	for _, tt := range tests {
		got := Port(tt.input)
		if got != tt.want {
			t.Errorf("Port(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestNonPostDoesNotParseBody(t *testing.T) {
	srv := setupServer(t)
	req := httptest.NewRequest("GET", "/v1/chat", nil)
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 404 {
		t.Fatalf("GET on POST-only endpoint should 404, got %d", w.Code)
	}
}

func TestRequestLoggerMiddleware(t *testing.T) {
	handler := requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, `{"ok":true}`)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
