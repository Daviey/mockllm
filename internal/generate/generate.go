package generate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ModelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	API    string                    `json:"api"`
	NPM    string                    `json:"npm"`
	Env    []string                  `json:"env"`
	Doc    string                    `json:"doc"`
	Models map[string]ModelsDevModel `json:"models"`
}

type ModelsDevModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Family      string `json:"family"`
	Attachment  bool   `json:"attachment"`
	Reasoning   bool   `json:"reasoning"`
	ToolCall    bool   `json:"tool_call"`
	Temperature bool   `json:"temperature"`
	OpenWeights bool   `json:"open_weights"`
	ReleaseDate string `json:"release_date"`
	Modalities  struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Cost struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
	} `json:"cost"`
	Limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	} `json:"limit"`
}

func FetchAPI(url string) (map[string]ModelsDevProvider, error) {
	if url == "" {
		url = "https://models.dev/api.json"
	}

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var providers map[string]ModelsDevProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return providers, nil
}

func LoadAPI(path string) (map[string]ModelsDevProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	var providers map[string]ModelsDevProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	return providers, nil
}

type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGemini    ProviderType = "gemini"
	ProviderUnknown   ProviderType = "unknown"
)

func classifyProvider(p ModelsDevProvider) ProviderType {
	switch {
	case p.ID == "openai":
		return ProviderOpenAI
	case p.ID == "anthropic":
		return ProviderAnthropic
	case p.ID == "google":
		return ProviderGemini
	case strings.Contains(p.NPM, "openai-compatible") || strings.Contains(p.API, "/v1"):
		return ProviderOpenAI
	case strings.Contains(p.NPM, "anthropic"):
		return ProviderAnthropic
	case strings.Contains(p.NPM, "google"):
		return ProviderGemini
	default:
		return ProviderOpenAI
	}
}

type GeneratedSpec struct {
	Filename string
	Content  []byte
}

func GenerateAll(providers map[string]ModelsDevProvider, include []string) []GeneratedSpec {
	var specs []GeneratedSpec

	for id, p := range providers {
		if len(include) > 0 {
			found := false
			for _, inc := range include {
				if id == inc {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		spec := generateProvider(id, p)
		specs = append(specs, spec)
	}

	sort.Slice(specs, func(i, j int) bool {
		return specs[i].Filename < specs[j].Filename
	})

	return specs
}

func generateProvider(id string, p ModelsDevProvider) GeneratedSpec {
	pt := classifyProvider(p)
	models := sortedModels(p.Models)

	var spec map[string]interface{}

	switch pt {
	case ProviderAnthropic:
		spec = buildAnthropicSpec(id, p, models)
	case ProviderGemini:
		spec = buildGeminiSpec(id, p, models)
	default:
		spec = buildOpenAISpec(id, p, models)
	}

	data, _ := json.MarshalIndent(spec, "", "  ")

	return GeneratedSpec{
		Filename: filepath.Join(id, id+".json"),
		Content:  append(data, '\n'),
	}
}

func sortedModels(m map[string]ModelsDevModel) []ModelsDevModel {
	models := make([]ModelsDevModel, 0, len(m))
	for _, model := range m {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})
	return models
}

func now() int64 {
	return time.Now().Unix()
}

func buildOpenAISpec(id string, p ModelsDevProvider, models []ModelsDevModel) map[string]interface{} {
	basePath := "/v1"
	if p.API != "" {
		basePath = extractPath(p.API)
	}

	modelList := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		modelList = append(modelList, map[string]interface{}{
			"id":       m.ID,
			"object":   "model",
			"created":  now(),
			"owned_by": id,
		})
	}

	mockModel := "gpt-4"
	if len(models) > 0 {
		mockModel = models[0].ID
	}

	return map[string]interface{}{
		"name":      id,
		"version":   "auto-generated",
		"base_path": basePath,
		"endpoints": []interface{}{
			map[string]interface{}{
				"path":   "/chat/completions",
				"method": "POST",
				"responses": []interface{}{
					map[string]interface{}{
						"status": 200,
						"label":  "success",
						"body": map[string]interface{}{
							"id":      "chatcmpl-dummymock",
							"object":  "chat.completion",
							"created": now(),
							"model":   mockModel,
							"choices": []interface{}{
								map[string]interface{}{
									"index": 0,
									"message": map[string]interface{}{
										"role":    "assistant",
										"content": "This is a mock response from mockllm.",
									},
									"finish_reason": "stop",
								},
							},
							"usage": map[string]interface{}{
								"prompt_tokens":     10,
								"completion_tokens": 10,
								"total_tokens":      20,
							},
						},
					},
					map[string]interface{}{
						"status": 200,
						"label":  "streaming",
						"stream": true,
						"stream_chunks": []interface{}{
							sseChunk(map[string]interface{}{
								"id":      "chatcmpl-dummymock",
								"object":  "chat.completion.chunk",
								"created": now(),
								"model":   mockModel,
								"choices": []interface{}{
									map[string]interface{}{"index": 0, "delta": map[string]interface{}{"role": "assistant", "content": ""}, "finish_reason": nil},
								},
							}, "50ms"),
							sseChunk(map[string]interface{}{
								"id":      "chatcmpl-dummymock",
								"object":  "chat.completion.chunk",
								"created": now(),
								"model":   mockModel,
								"choices": []interface{}{
									map[string]interface{}{"index": 0, "delta": map[string]interface{}{"content": "This is a mock streaming response."}, "finish_reason": nil},
								},
							}, "50ms"),
							sseChunk(map[string]interface{}{
								"id":      "chatcmpl-dummymock",
								"object":  "chat.completion.chunk",
								"created": now(),
								"model":   mockModel,
								"choices": []interface{}{
									map[string]interface{}{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "stop"},
								},
							}, "50ms"),
						},
					},
				},
			},
			map[string]interface{}{
				"path":       "/chat/completions/errors/sequential",
				"method":     "POST",
				"match_mode": "sequential",
				"responses": []interface{}{
					map[string]interface{}{"status": 200, "label": "success", "body": map[string]interface{}{
						"id": "chatcmpl-dummymock", "object": "chat.completion", "created": now(), "model": mockModel,
						"choices": []interface{}{map[string]interface{}{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}},
						"usage":   map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
					}},
					map[string]interface{}{"status": 429, "label": "rate_limit", "headers": map[string]string{"Retry-After": "2"}, "body": map[string]interface{}{
						"error": map[string]interface{}{"message": "Rate limit exceeded.", "type": "rate_limit_error", "code": "rate_limit_exceeded"},
					}},
					map[string]interface{}{"status": 500, "label": "server_error", "body": map[string]interface{}{
						"error": map[string]interface{}{"message": "Internal server error.", "type": "server_error"},
					}},
				},
			},
			map[string]interface{}{
				"path":       "/chat/completions/errors/weighted",
				"method":     "POST",
				"match_mode": "weighted",
				"responses": []interface{}{
					map[string]interface{}{"status": 200, "weight": 80, "label": "success", "body": map[string]interface{}{
						"id": "chatcmpl-dummymock", "object": "chat.completion", "created": now(), "model": mockModel,
						"choices": []interface{}{map[string]interface{}{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "OK"}, "finish_reason": "stop"}},
						"usage":   map[string]interface{}{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
					}},
					map[string]interface{}{"status": 429, "weight": 12, "label": "rate_limit", "body": map[string]interface{}{
						"error": map[string]interface{}{"message": "Rate limit exceeded.", "type": "rate_limit_error"},
					}},
					map[string]interface{}{"status": 500, "weight": 5, "label": "server_error", "body": map[string]interface{}{
						"error": map[string]interface{}{"message": "Internal server error.", "type": "server_error"},
					}},
					map[string]interface{}{"status": 503, "weight": 3, "label": "overloaded", "body": map[string]interface{}{
						"error": map[string]interface{}{"message": "Service overloaded.", "type": "server_error"},
					}},
				},
			},
			map[string]interface{}{
				"path":   "/models",
				"method": "GET",
				"responses": []interface{}{
					map[string]interface{}{
						"status": 200,
						"body": map[string]interface{}{
							"object": "list",
							"data":   modelList,
						},
					},
				},
			},
		},
	}
}

func buildAnthropicSpec(id string, p ModelsDevProvider, models []ModelsDevModel) map[string]interface{} {
	mockModel := "claude-3-opus-20240229"
	if len(models) > 0 {
		mockModel = models[0].ID
	}

	return map[string]interface{}{
		"name":      id,
		"version":   "auto-generated",
		"base_path": "",
		"endpoints": []interface{}{
			map[string]interface{}{
				"path":   "/v1/messages",
				"method": "POST",
				"responses": []interface{}{
					map[string]interface{}{
						"status":  200,
						"label":   "success",
						"headers": map[string]string{"anthropic-ratelimit-requests-remaining": "999"},
						"body": map[string]interface{}{
							"id":          "msg_dummy_mock",
							"type":        "message",
							"role":        "assistant",
							"content":     []interface{}{map[string]interface{}{"type": "text", "text": "This is a mock response from mockllm."}},
							"model":       mockModel,
							"stop_reason": "end_turn",
							"usage":       map[string]interface{}{"input_tokens": 10, "output_tokens": 10},
						},
					},
					map[string]interface{}{
						"status": 200,
						"label":  "streaming",
						"stream": true,
						"stream_chunks": []interface{}{
							sseChunk(map[string]interface{}{"type": "message_start", "message": map[string]interface{}{
								"id": "msg_dummy_mock", "type": "message", "role": "assistant", "content": []interface{}{}, "model": mockModel, "usage": map[string]interface{}{"input_tokens": 10, "output_tokens": 0},
							}}, "50ms"),
							sseChunk(map[string]interface{}{"type": "content_block_start", "index": 0, "content_block": map[string]interface{}{"type": "text", "text": ""}}, "50ms"),
							sseChunk(map[string]interface{}{"type": "content_block_delta", "index": 0, "delta": map[string]interface{}{"type": "text_delta", "text": "This is a mock streaming response."}}, "50ms"),
							sseChunk(map[string]interface{}{"type": "content_block_stop", "index": 0}, "50ms"),
							sseChunk(map[string]interface{}{"type": "message_delta", "delta": map[string]interface{}{"stop_reason": "end_turn"}, "usage": map[string]interface{}{"output_tokens": 10}}, "50ms"),
							sseChunk(map[string]interface{}{"type": "message_stop"}, "10ms"),
						},
					},
				},
			},
			map[string]interface{}{
				"path":       "/v1/messages/errors/sequential",
				"method":     "POST",
				"match_mode": "sequential",
				"responses": []interface{}{
					map[string]interface{}{"status": 200, "label": "success", "body": map[string]interface{}{
						"id": "msg_dummy_mock", "type": "message", "role": "assistant",
						"content": []interface{}{map[string]interface{}{"type": "text", "text": "OK"}},
						"model":   mockModel, "stop_reason": "end_turn", "usage": map[string]interface{}{"input_tokens": 5, "output_tokens": 1},
					}},
					map[string]interface{}{"status": 429, "label": "rate_limit", "body": map[string]interface{}{
						"type": "error", "error": map[string]interface{}{"type": "rate_limit_error", "message": "Rate limit exceeded."},
					}},
					map[string]interface{}{"status": 529, "label": "overloaded", "body": map[string]interface{}{
						"type": "error", "error": map[string]interface{}{"type": "overloaded_error", "message": "Overloaded."},
					}},
				},
			},
			map[string]interface{}{
				"path":       "/v1/messages/errors/weighted",
				"method":     "POST",
				"match_mode": "weighted",
				"responses": []interface{}{
					map[string]interface{}{"status": 200, "weight": 80, "label": "success", "body": map[string]interface{}{
						"id": "msg_dummy_mock", "type": "message", "role": "assistant",
						"content": []interface{}{map[string]interface{}{"type": "text", "text": "OK"}},
						"model":   mockModel, "stop_reason": "end_turn", "usage": map[string]interface{}{"input_tokens": 5, "output_tokens": 1},
					}},
					map[string]interface{}{"status": 429, "weight": 10, "label": "rate_limit", "body": map[string]interface{}{
						"type": "error", "error": map[string]interface{}{"type": "rate_limit_error", "message": "Rate limit exceeded."},
					}},
					map[string]interface{}{"status": 529, "weight": 5, "label": "overloaded", "body": map[string]interface{}{
						"type": "error", "error": map[string]interface{}{"type": "overloaded_error", "message": "Overloaded."},
					}},
					map[string]interface{}{"status": 500, "weight": 5, "label": "server_error", "body": map[string]interface{}{
						"type": "error", "error": map[string]interface{}{"type": "api_error", "message": "Internal server error."},
					}},
				},
			},
		},
	}
}

func buildGeminiSpec(id string, p ModelsDevProvider, models []ModelsDevModel) map[string]interface{} {
	mockModel := "gemini-pro"
	if len(models) > 0 {
		mockModel = models[0].ID
	}

	modelList := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		modelList = append(modelList, map[string]interface{}{
			"name":                       "models/" + m.ID,
			"displayName":                m.Name,
			"inputTokenLimit":            m.Limit.Context,
			"outputTokenLimit":           m.Limit.Output,
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
		})
	}

	return map[string]interface{}{
		"name":      id,
		"version":   "auto-generated",
		"base_path": "/v1beta",
		"endpoints": []interface{}{
			map[string]interface{}{
				"path":   fmt.Sprintf("/models/%s:generateContent", mockModel),
				"method": "POST",
				"responses": []interface{}{
					map[string]interface{}{
						"status": 200,
						"label":  "success",
						"body": map[string]interface{}{
							"candidates": []interface{}{
								map[string]interface{}{
									"content":      map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "This is a mock response from mockllm."}}, "role": "model"},
									"finishReason": "STOP",
									"index":        0,
								},
							},
							"usageMetadata": map[string]interface{}{"promptTokenCount": 10, "candidatesTokenCount": 10, "totalTokenCount": 20},
						},
					},
				},
			},
			map[string]interface{}{
				"path":   fmt.Sprintf("/models/%s:streamGenerateContent", mockModel),
				"method": "POST",
				"responses": []interface{}{
					map[string]interface{}{
						"status": 200,
						"stream": true,
						"stream_chunks": []interface{}{
							sseChunk(map[string]interface{}{
								"candidates": []interface{}{map[string]interface{}{
									"content":      map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "This is a mock streaming response."}}, "role": "model"},
									"finishReason": "STOP", "index": 0,
								}},
							}, "50ms"),
						},
					},
				},
			},
			map[string]interface{}{
				"path":       fmt.Sprintf("/models/%s:generateContent/errors/sequential", mockModel),
				"method":     "POST",
				"match_mode": "sequential",
				"responses": []interface{}{
					map[string]interface{}{"status": 200, "label": "success", "body": map[string]interface{}{
						"candidates":    []interface{}{map[string]interface{}{"content": map[string]interface{}{"parts": []interface{}{map[string]interface{}{"text": "OK"}}, "role": "model"}, "finishReason": "STOP", "index": 0}},
						"usageMetadata": map[string]interface{}{"promptTokenCount": 5, "candidatesTokenCount": 1, "totalTokenCount": 6},
					}},
					map[string]interface{}{"status": 429, "label": "rate_limit", "body": map[string]interface{}{
						"error": map[string]interface{}{"code": 429, "message": "Resource exhausted", "status": "RESOURCE_EXHAUSTED"},
					}},
					map[string]interface{}{"status": 503, "label": "unavailable", "body": map[string]interface{}{
						"error": map[string]interface{}{"code": 503, "message": "The model is overloaded", "status": "UNAVAILABLE"},
					}},
				},
			},
			map[string]interface{}{
				"path":   "/models",
				"method": "GET",
				"responses": []interface{}{
					map[string]interface{}{
						"status": 200,
						"body":   map[string]interface{}{"models": modelList},
					},
				},
			},
		},
	}
}

func sseChunk(data map[string]interface{}, delay string) map[string]interface{} {
	return map[string]interface{}{
		"data":  data,
		"delay": delay,
	}
}

func extractPath(apiURL string) string {
	apiURL = strings.TrimPrefix(apiURL, "https://")
	apiURL = strings.TrimPrefix(apiURL, "http://")
	idx := strings.Index(apiURL, "/")
	if idx == -1 {
		return "/v1"
	}
	return apiURL[idx:]
}

func WriteSpecs(dir string, specs []GeneratedSpec) error {
	for _, spec := range specs {
		path := filepath.Join(dir, spec.Filename)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("creating dir for %s: %w", spec.Filename, err)
		}
		if err := os.WriteFile(path, spec.Content, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", spec.Filename, err)
		}
	}
	return nil
}
