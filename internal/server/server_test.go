package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestStreamingBodyFallback(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/stream-body",
				Method: "POST",
				Responses: []provider.Response{
					{
						Status: 200,
						Stream: true,
						Body:   json.RawMessage(`{"text":"hello from body"}`),
					},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("POST", "/stream-body", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, `{"text":"hello from body"}`) {
		t.Fatalf("expected body content in SSE, got %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatal("expected [DONE]")
	}
}

func TestStreamingWithDelay(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/stream-delay",
				Method: "POST",
				Responses: []provider.Response{
					{
						Status: 200,
						Stream: true,
						Delay:  "1ms",
						StreamChunks: []provider.StreamChunk{
							{Data: json.RawMessage(`{"a":1}`), Delay: "1ms"},
							{Data: json.RawMessage(`{"b":2}`)},
						},
					},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("POST", "/stream-delay", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, `{"a":1}`) || !strings.Contains(body, `{"b":2}`) {
		t.Fatalf("expected both chunks, got %s", body)
	}
}

func TestStreamingWithHeaders(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/stream-headers",
				Method: "POST",
				Responses: []provider.Response{
					{
						Status:  200,
						Stream:  true,
						Headers: map[string]string{"X-Stream-Custom": "yes"},
						StreamChunks: []provider.StreamChunk{
							{Data: json.RawMessage(`{"ok":true}`)},
						},
					},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("POST", "/stream-headers", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if v := w.Header().Get("X-Stream-Custom"); v != "yes" {
		t.Fatalf("expected X-Stream-Custom=yes, got %s", v)
	}
}

func TestDelayResponse(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/delayed",
				Method: "POST",
				Responses: []provider.Response{
					{
						Status: 200,
						Delay:  "10ms",
						Body:   json.RawMessage(`{"delayed":true}`),
					},
				},
			},
		},
	})
	srv := New(r, 0)

	start := time.Now()
	req := httptest.NewRequest("POST", "/delayed", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)
	elapsed := time.Since(start)

	if elapsed < 10*time.Millisecond {
		t.Fatalf("expected delay >= 10ms, got %v", elapsed)
	}
}

func TestCORSPassThrough(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/anything", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 for non-OPTIONS, got %d", w.Code)
	}
	if origin := w.Header().Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("expected CORS header on all requests, got %s", origin)
	}
}

func TestStatusWriterAuto200(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: 200}

	sw.Write([]byte("hello"))

	if sw.status != 200 {
		t.Fatalf("expected auto-set status 200, got %d", sw.status)
	}
}

func TestNilResponseBody(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/nil-response",
				Method: "POST",
				Responses: []provider.Response{
					{Status: 200},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("POST", "/nil-response", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestInvalidDelayIgnored(t *testing.T) {
	r := provider.NewRegistry()
	r.Register(&provider.Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []provider.Endpoint{
			{
				Path:   "/bad-delay",
				Method: "POST",
				Responses: []provider.Response{
					{Status: 200, Delay: "not-a-duration", Body: json.RawMessage(`{}`)},
				},
			},
		},
	})
	srv := New(r, 0)

	req := httptest.NewRequest("POST", "/bad-delay", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 even with bad delay, got %d", w.Code)
	}
}
