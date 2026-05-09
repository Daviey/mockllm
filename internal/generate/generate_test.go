package generate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testProvider(id, npm, api string, models map[string]ModelsDevModel) ModelsDevProvider {
	return ModelsDevProvider{
		ID:     id,
		Name:   id,
		API:    api,
		NPM:    npm,
		Models: models,
	}
}

func testModel(id, name string) ModelsDevModel {
	return ModelsDevModel{
		ID:   id,
		Name: name,
		Cost: struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
		}{Input: 1.0, Output: 2.0},
		Limit: struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		}{Context: 4096, Output: 1024},
	}
}

func TestClassifyProvider(t *testing.T) {
	tests := []struct {
		id   string
		npm  string
		api  string
		want ProviderType
	}{
		{"openai", "@ai-sdk/openai", "", ProviderOpenAI},
		{"anthropic", "@ai-sdk/anthropic", "", ProviderAnthropic},
		{"google", "@ai-sdk/google", "", ProviderGemini},
		{"groq", "@ai-sdk/openai-compatible", "https://api.groq.com/v1", ProviderOpenAI},
		{"some-provider", "@ai-sdk/openai-compatible", "", ProviderOpenAI},
		{"other", "@ai-sdk/anthropic-thing", "", ProviderAnthropic},
		{"gcloud", "@ai-sdk/google-vertex", "", ProviderGemini},
		{"unknown", "@ai-sdk/other", "", ProviderOpenAI},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			p := testProvider(tt.id, tt.npm, tt.api, nil)
			got := classifyProvider(p)
			if got != tt.want {
				t.Errorf("classifyProvider(%s) = %s, want %s", tt.id, got, tt.want)
			}
		})
	}
}

func TestGenerateAll(t *testing.T) {
	providers := map[string]ModelsDevProvider{
		"openai":    testProvider("openai", "@ai-sdk/openai", "", map[string]ModelsDevModel{"gpt-4": testModel("gpt-4", "GPT-4")}),
		"anthropic": testProvider("anthropic", "@ai-sdk/anthropic", "", map[string]ModelsDevModel{"claude-3": testModel("claude-3", "Claude 3")}),
		"google":    testProvider("google", "@ai-sdk/google", "", map[string]ModelsDevModel{"gemini-pro": testModel("gemini-pro", "Gemini Pro")}),
	}

	t.Run("all providers", func(t *testing.T) {
		specs := GenerateAll(providers, nil)
		if len(specs) != 3 {
			t.Fatalf("expected 3 specs, got %d", len(specs))
		}
		for _, spec := range specs {
			if len(spec.Content) == 0 {
				t.Errorf("spec %s has empty content", spec.Filename)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(spec.Content, &parsed); err != nil {
				t.Errorf("spec %s is not valid JSON: %v", spec.Filename, err)
			}
		}
	})

	t.Run("filtered providers", func(t *testing.T) {
		specs := GenerateAll(providers, []string{"openai"})
		if len(specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(specs))
		}
		if specs[0].Filename != "openai/openai.json" {
			t.Errorf("expected openai/openai.json, got %s", specs[0].Filename)
		}
	})

	t.Run("empty filter returns all", func(t *testing.T) {
		specs := GenerateAll(providers, []string{})
		if len(specs) != 3 {
			t.Fatalf("expected 3 specs with empty filter, got %d", len(specs))
		}
	})
}

func TestBuildOpenAISpec(t *testing.T) {
	p := testProvider("test-openai", "@ai-sdk/openai", "https://api.example.com/v1", map[string]ModelsDevModel{
		"model-a": testModel("model-a", "Model A"),
		"model-b": testModel("model-b", "Model B"),
	})
	models := sortedModels(p.Models)
	spec := buildOpenAISpec("test-openai", p, models)

	if spec["name"] != "test-openai" {
		t.Errorf("expected name=test-openai, got %v", spec["name"])
	}
	if spec["base_path"] != "/v1" {
		t.Errorf("expected base_path=/v1, got %v", spec["base_path"])
	}

	endpoints := spec["endpoints"].([]interface{})
	paths := []string{}
	for _, ep := range endpoints {
		m := ep.(map[string]interface{})
		paths = append(paths, m["path"].(string))
	}

	expectedPaths := []string{"/chat/completions", "/chat/completions/errors/sequential", "/chat/completions/errors/weighted", "/models"}
	for _, expected := range expectedPaths {
		found := false
		for _, p := range paths {
			if p == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing endpoint path %s in %v", expected, paths)
		}
	}

	for _, ep := range endpoints {
		m := ep.(map[string]interface{})
		if m["path"] == "/models" {
			resps := m["responses"].([]interface{})
			body := resps[0].(map[string]interface{})["body"].(map[string]interface{})
			data := body["data"].([]map[string]interface{})
			if len(data) != 2 {
				t.Errorf("expected 2 models, got %d", len(data))
			}
		}
	}
}

func TestBuildAnthropicSpec(t *testing.T) {
	p := testProvider("anthropic", "@ai-sdk/anthropic", "", map[string]ModelsDevModel{
		"claude-3-opus": testModel("claude-3-opus", "Claude 3 Opus"),
	})
	models := sortedModels(p.Models)
	spec := buildAnthropicSpec("anthropic", p, models)

	if spec["base_path"] != "" {
		t.Errorf("expected empty base_path, got %v", spec["base_path"])
	}

	endpoints := spec["endpoints"].([]interface{})
	paths := []string{}
	for _, ep := range endpoints {
		m := ep.(map[string]interface{})
		paths = append(paths, m["path"].(string))
	}

	if paths[0] != "/v1/messages" {
		t.Errorf("expected first endpoint /v1/messages, got %s", paths[0])
	}
}

func TestBuildGeminiSpec(t *testing.T) {
	p := testProvider("google", "@ai-sdk/google", "", map[string]ModelsDevModel{
		"gemini-pro": testModel("gemini-pro", "Gemini Pro"),
	})
	models := sortedModels(p.Models)
	spec := buildGeminiSpec("google", p, models)

	if spec["base_path"] != "/v1beta" {
		t.Errorf("expected base_path=/v1beta, got %v", spec["base_path"])
	}

	endpoints := spec["endpoints"].([]interface{})
	firstPath := endpoints[0].(map[string]interface{})["path"].(string)
	if firstPath != "/models/gemini-pro:generateContent" {
		t.Errorf("expected model-specific path, got %s", firstPath)
	}
}

func TestBuildOpenAISpecNoModels(t *testing.T) {
	p := testProvider("empty", "@ai-sdk/openai", "", map[string]ModelsDevModel{})
	spec := buildOpenAISpec("empty", p, nil)

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty spec")
	}

	endpoints := spec["endpoints"].([]interface{})
	chatEp := endpoints[0].(map[string]interface{})
	resps := chatEp["responses"].([]interface{})
	body := resps[0].(map[string]interface{})["body"].(map[string]interface{})
	if body["model"] != "gpt-4" {
		t.Errorf("expected fallback model gpt-4, got %v", body["model"])
	}
}

func TestExtractPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://api.example.com/v1", "/v1"},
		{"https://api.example.com/v2/custom", "/v2/custom"},
		{"http://localhost:8080/api", "/api"},
		{"https://no-path.com", "/v1"},
		{"", "/v1"},
	}

	for _, tt := range tests {
		got := extractPath(tt.input)
		if got != tt.want {
			t.Errorf("extractPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteSpecs(t *testing.T) {
	dir := t.TempDir()
	specs := []GeneratedSpec{
		{Filename: "test1/test1.json", Content: []byte(`{"name":"test1"}`)},
		{Filename: "test2/test2.json", Content: []byte(`{"name":"test2"}`)},
	}

	if err := WriteSpecs(dir, specs); err != nil {
		t.Fatal(err)
	}

	for _, spec := range specs {
		path := filepath.Join(dir, spec.Filename)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("file %s not written: %v", spec.Filename, err)
		}
		if string(data) != string(spec.Content) {
			t.Errorf("file %s content mismatch", spec.Filename)
		}
	}
}

func TestWriteSpecsBadDir(t *testing.T) {
	err := WriteSpecs("/proc/fake/impossible/path", []GeneratedSpec{
		{Filename: "test/test.json", Content: []byte(`{}`)},
	})
	if err == nil {
		t.Fatal("expected error writing to invalid path")
	}
}

func TestLoadAPI(t *testing.T) {
	dir := t.TempDir()
	data := map[string]ModelsDevProvider{
		"test": testProvider("test", "@ai-sdk/openai", "https://api.test.com/v1", map[string]ModelsDevModel{
			"model-1": testModel("model-1", "Model One"),
		}),
	}
	raw, _ := json.Marshal(data)
	path := filepath.Join(dir, "api.json")
	_ = os.WriteFile(path, raw, 0644)

	loaded, err := LoadAPI(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(loaded))
	}
	if loaded["test"].Name != "test" {
		t.Errorf("expected name=test, got %s", loaded["test"].Name)
	}
	if len(loaded["test"].Models) != 1 {
		t.Errorf("expected 1 model, got %d", len(loaded["test"].Models))
	}
}

func TestLoadAPIFileNotFound(t *testing.T) {
	_, err := LoadAPI("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadAPIInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(path, []byte(`not json`), 0644)

	_, err := LoadAPI(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetchAPIWithTestServer(t *testing.T) {
	data := map[string]ModelsDevProvider{
		"test": testProvider("test", "@ai-sdk/openai", "", nil),
	}
	raw, _ := json.Marshal(data)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer ts.Close()

	providers, err := FetchAPI(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
}

func TestFetchAPIBadStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer ts.Close()

	_, err := FetchAPI(ts.URL)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestFetchAPIEmptyURL(t *testing.T) {
	providers, err := FetchAPI("")
	if err != nil {
		t.Fatalf("empty URL should default to models.dev: %v", err)
	}
	if len(providers) == 0 {
		t.Fatal("expected providers from models.dev")
	}
}

func TestSortedModels(t *testing.T) {
	models := map[string]ModelsDevModel{
		"c": testModel("c", "Charlie"),
		"a": testModel("a", "Alice"),
		"b": testModel("b", "Bob"),
	}
	sorted := sortedModels(models)
	if sorted[0].Name != "Alice" || sorted[1].Name != "Bob" || sorted[2].Name != "Charlie" {
		t.Errorf("expected alphabetical sort, got %v", []string{sorted[0].Name, sorted[1].Name, sorted[2].Name})
	}
}

func TestNow(t *testing.T) {
	ts := now()
	if ts <= 0 {
		t.Errorf("expected positive timestamp, got %d", ts)
	}
}
