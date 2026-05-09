package provider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestSpec(t *testing.T, spec Provider) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, spec.Name+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadDir(t *testing.T) {
	spec := Provider{
		Name:     "test",
		Version:  "1.0",
		BasePath: "/v1",
		Endpoints: []Endpoint{
			{
				Path:   "/chat",
				Method: "POST",
				Responses: []Response{
					{Status: 200, Body: json.RawMessage(`{"ok":true}`)},
				},
			},
		},
	}

	dir := writeTestSpec(t, spec)
	r := NewRegistry()
	if err := r.LoadDir(dir); err != nil {
		t.Fatal(err)
	}

	providers := r.Providers()
	if len(providers) != 1 || providers[0] != "test" {
		t.Fatalf("expected [test], got %v", providers)
	}
}

func TestMatchDefault(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "/v1",
		Endpoints: []Endpoint{
			{
				Path:   "/chat",
				Method: "POST",
				Responses: []Response{
					{Status: 200, Body: json.RawMessage(`{"ok":true}`), Label: "ok"},
					{Status: 200, Stream: true, StreamChunks: []StreamChunk{
						{Data: json.RawMessage(`{"chunk":1}`)},
					}, Label: "stream"},
				},
			},
		},
	})

	_, resp, found := r.Match("POST", "/v1/chat", false)
	if !found {
		t.Fatal("expected match")
	}
	if resp.Label != "ok" {
		t.Fatalf("expected ok label, got %s", resp.Label)
	}

	_, resp, found = r.Match("POST", "/v1/chat", true)
	if !found {
		t.Fatal("expected match")
	}
	if resp.Label != "stream" {
		t.Fatalf("expected stream label, got %s", resp.Label)
	}
}

func TestMatchNotFound(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "/v1",
		Endpoints: []Endpoint{
			{Path: "/chat", Method: "POST", Responses: []Response{
				{Status: 200, Body: json.RawMessage(`{}`)},
			}},
		},
	})

	_, _, found := r.Match("GET", "/v1/nonexistent", false)
	if found {
		t.Fatal("expected no match")
	}
}

func TestSequentialMode(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{
				Path:      "/seq",
				Method:    "POST",
				MatchMode: "sequential",
				Responses: []Response{
					{Status: 200, Label: "first"},
					{Status: 429, Label: "second"},
					{Status: 500, Label: "third"},
				},
			},
		},
	})

	expected := []string{"first", "second", "third", "first"}
	for _, want := range expected {
		_, resp, _ := r.Match("POST", "/seq", false)
		if resp.Label != want {
			t.Fatalf("expected %s, got %s (status %d)", want, resp.Label, resp.Status)
		}
	}
}

func TestReset(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{
				Path:      "/seq",
				Method:    "POST",
				MatchMode: "sequential",
				Responses: []Response{
					{Status: 200, Label: "first"},
					{Status: 500, Label: "second"},
				},
			},
		},
	})

	_, resp, _ := r.Match("POST", "/seq", false)
	if resp.Label != "first" {
		t.Fatal("expected first")
	}

	_, resp, _ = r.Match("POST", "/seq", false)
	if resp.Label != "second" {
		t.Fatal("expected second")
	}

	r.Reset()

	_, resp, _ = r.Match("POST", "/seq", false)
	if resp.Label != "first" {
		t.Fatalf("expected first after reset, got %s", resp.Label)
	}
}

func TestWeightedMode(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{
				Path:      "/weighted",
				Method:    "POST",
				MatchMode: "weighted",
				Responses: []Response{
					{Status: 200, Weight: 100, Label: "success"},
					{Status: 500, Weight: 0, Label: "never"},
				},
			},
		},
	})

	for i := 0; i < 50; i++ {
		_, resp, _ := r.Match("POST", "/weighted", false)
		if resp.Label != "success" {
			t.Fatalf("weight 0 response should never be selected, got %s on call %d", resp.Label, i)
		}
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		spec    Provider
		wantErr string
	}{
		{
			name: "missing name",
			spec: Provider{Name: "", Endpoints: []Endpoint{
				{Path: "/test", Method: "GET", Responses: []Response{{Status: 200}}},
			}},
			wantErr: "name is required",
		},
		{
			name: "missing path",
			spec: Provider{Name: "test", Endpoints: []Endpoint{
				{Path: "", Method: "GET", Responses: []Response{{Status: 200}}},
			}},
			wantErr: "path is required",
		},
		{
			name: "missing method",
			spec: Provider{Name: "test", Endpoints: []Endpoint{
				{Path: "/test", Method: "", Responses: []Response{{Status: 200}}},
			}},
			wantErr: "method is required",
		},
		{
			name: "no responses",
			spec: Provider{Name: "test", Endpoints: []Endpoint{
				{Path: "/test", Method: "GET", Responses: []Response{}},
			}},
			wantErr: "at least one response is required",
		},
		{
			name: "invalid status",
			spec: Provider{Name: "test", Endpoints: []Endpoint{
				{Path: "/test", Method: "GET", Responses: []Response{{Status: 999}}},
			}},
			wantErr: "invalid status code",
		},
		{
			name: "weighted no weights",
			spec: Provider{Name: "test", Endpoints: []Endpoint{
				{Path: "/test", Method: "GET", MatchMode: "weighted", Responses: []Response{
					{Status: 200, Weight: 0},
				}},
			}},
			wantErr: "weighted mode requires",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProvider(&tt.spec)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestCompactJSON(t *testing.T) {
	dir := t.TempDir()
	pretty := `{"name":"test","base_path":"/v1","endpoints":[{"path":"/chat","method":"POST","responses":[{"status":200,"body":  {"hello"  :  "world"  }}]}]}`
	path := filepath.Join(dir, "test.json")
	if err := os.WriteFile(path, []byte(pretty), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	if err := r.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	_, resp, _ := r.Match("POST", "/v1/chat", false)
	if resp == nil {
		t.Fatal("expected response")
	}
	if string(resp.Body) != `{"hello":"world"}` {
		t.Fatalf("expected compact JSON, got %s", string(resp.Body))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && searchString(s, sub)))
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestLoadDirInvalidPath(t *testing.T) {
	r := NewRegistry()
	err := r.LoadDir("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestLoadFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`not json`), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	err := r.LoadFile(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadFileValidationFail(t *testing.T) {
	dir := t.TempDir()
	data := `{"name":"","endpoints":[{"path":"/test","method":"GET","responses":[{"status":200}]}]}`
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	err := r.LoadFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCompactJSONNil(t *testing.T) {
	result := compactJSON(nil)
	if result != nil {
		t.Fatalf("expected nil, got %s", result)
	}
}

func TestCompactJSONInvalid(t *testing.T) {
	result := compactJSON(json.RawMessage(`not valid json {`))
	if string(result) != `not valid json {` {
		t.Fatalf("expected passthrough of invalid JSON, got %s", result)
	}
}

func TestMatchMethodInsensitive(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{Path: "/test", Method: "post", Responses: []Response{
				{Status: 200, Body: json.RawMessage(`{}`)},
			}},
		},
	})

	_, _, found := r.Match("POST", "/test", false)
	if !found {
		t.Fatal("expected match with uppercase POST for lowercase post method")
	}
}

func TestSelectResponseDefaultNonStream(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{Path: "/test", Method: "POST", Responses: []Response{
				{Status: 200, Stream: true, Body: json.RawMessage(`{"stream":true}`)},
				{Status: 200, Body: json.RawMessage(`{"stream":false}`)},
			}},
		},
	})

	_, resp, _ := r.Match("POST", "/test", false)
	if resp.Stream {
		t.Fatal("expected non-stream response when wantsStream=false")
	}
}

func TestWeightedAllZeroFallsToFirst(t *testing.T) {
	r := NewRegistry()
	r.Register(&Provider{
		Name:     "test",
		BasePath: "",
		Endpoints: []Endpoint{
			{Path: "/test", Method: "POST", MatchMode: "weighted", Responses: []Response{
				{Status: 200, Weight: 0, Label: "first"},
				{Status: 500, Weight: 0, Label: "second"},
			}},
		},
	})

	for i := 0; i < 10; i++ {
		_, resp, _ := r.Match("POST", "/test", false)
		if resp.Label != "first" {
			t.Fatalf("all-zero weights should return first, got %s on call %d", resp.Label, i)
		}
	}
}
