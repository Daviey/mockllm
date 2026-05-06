# mockllm

A deterministic mock server for LLM APIs. Drop in JSON spec files to define provider endpoints and get predictable, consistent responses for testing.

**Auto-generates specs from [models.dev](https://models.dev)** — 118+ providers, always up to date.

## Why?

When testing applications that call LLM APIs, you need:
- **Deterministic responses** — same request, same response, every time
- **Error simulation** — test your retry logic, rate limit handling, and resilience
- **No API costs** — no real API calls during tests
- **Offline testing** — works without internet
- **Multiple providers** — one server for OpenAI, Anthropic, Gemini, and 115+ more

## Quick Start

```bash
# Build
make build

# Run with bundled providers
./mockllm

# Generate fresh specs from models.dev and run
make generate
./mockllm -specs ./providers

# Generate specs for specific providers
go run ./cmd/mockllm-gen -providers openai,anthropic,google -output ./providers

# Run with custom port
./mockllm -port 9090 -specs ./my-specs
```

## Usage

Point your LLM client at the server:

```bash
# OpenAI-compatible request
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'

# Anthropic-compatible request
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: any-key" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-3-opus-20240229","messages":[{"role":"user","content":"hello"}],"max_tokens":100}'

# Gemini-compatible request
curl http://localhost:8080/v1beta/models/gemini-pro:generateContent \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"parts":[{"text":"hello"}]}]}'
```

## Spec Generation from models.dev

mockllm can auto-generate provider specs from the [models.dev](https://models.dev) open-source database:

```bash
# Generate for all 118+ providers
go run ./cmd/mockllm-gen -output ./providers

# Generate for specific providers
go run ./cmd/mockllm-gen -providers openai,anthropic,google,deepseek

# Use a local api.json file
go run ./cmd/mockllm-gen -source ./api.json -output ./providers
```

Generated specs include:
- Realistic model lists from the models.dev database
- OpenAI, Anthropic, and Gemini API response shapes (auto-detected by provider type)
- Error simulation endpoints (sequential + weighted)
- Streaming support

To keep specs up to date, run generation in CI or as a scheduled job.

## Provider Spec Format

Each provider is defined by a JSON file in the `providers/` directory:

```json
{
  "name": "my-provider",
  "version": "1.0",
  "base_path": "/v1",
  "endpoints": [
    {
      "path": "/chat/completions",
      "method": "POST",
      "responses": [
        {
          "status": 200,
          "body": {
            "id": "chatcmpl-mock",
            "choices": [{ "message": { "role": "assistant", "content": "Hello!" } }]
          }
        }
      ]
    }
  ]
}
```

### Response Modes

By default, endpoints with multiple responses use the first non-streaming response for regular requests and the first streaming response for streaming requests. You can control response selection with `match_mode`:

#### Sequential Rotation (`"match_mode": "sequential"`)

Cycle through responses in order. Each request returns the next response, wrapping around. Perfect for deterministic error testing — simulate "success, rate limit, server error, repeat":

```json
{
  "path": "/chat/completions",
  "method": "POST",
  "match_mode": "sequential",
  "responses": [
    { "status": 200, "label": "success", "body": { "result": "ok" } },
    { "status": 429, "label": "rate_limit", "body": { "error": { "message": "rate limit exceeded" } } },
    { "status": 500, "label": "server_error", "body": { "error": { "message": "internal error" } } }
  ]
}
```

Reset the counter with `POST /_reset` to start the sequence over.

#### Weighted Random (`"match_mode": "weighted"`)

Non-deterministic selection based on weights. Use for simulating realistic failure rates in load/stress tests:

```json
{
  "path": "/chat/completions",
  "method": "POST",
  "match_mode": "weighted",
  "responses": [
    { "status": 200, "weight": 80, "label": "success", "body": { "result": "ok" } },
    { "status": 429, "weight": 12, "label": "rate_limit", "body": { "error": { "message": "rate limit" } } },
    { "status": 500, "weight": 5, "label": "server_error", "body": { "error": { "message": "internal" } } },
    { "status": 503, "weight": 3, "label": "overloaded", "body": { "error": { "message": "overloaded" } } }
  ]
}
```

Weights are relative — `80:12:5:3` means ~80% success, ~12% rate limit, ~5% server error, ~3% overloaded.

### Streaming

Add `stream_chunks` to get SSE streaming. When a request includes `"stream": true`, the server automatically selects a streaming response:

```json
{
  "status": 200,
  "stream": true,
  "stream_chunks": [
    {
      "delay": "50ms",
      "data": { "choices": [{ "delta": { "content": "Hello" } }] }
    },
    {
      "delay": "50ms",
      "data": { "choices": [{ "delta": { "content": " world" } }] }
    }
  ]
}
```

### Custom Headers & Delays

```json
{
  "status": 200,
  "delay": "200ms",
  "headers": {
    "X-Custom-Header": "value"
  },
  "body": { "result": "delayed response" }
}
```

### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `status` | int | HTTP status code |
| `body` | object | JSON response body |
| `stream` | bool | Mark as streaming response |
| `stream_chunks` | array | SSE chunks with optional delays |
| `headers` | object | Custom response headers |
| `delay` | string | Go duration (e.g., `"200ms"`, `"1s"`) |
| `weight` | int | Probability weight (weighted mode only) |
| `label` | string | Human-readable label for the response |

## Built-in Providers

| Provider   | Endpoints |
|-----------|-----------|
| **OpenAI** | `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/models` |
| **Anthropic** | `/v1/messages` (with SSE streaming) |
| **Gemini** | `/v1beta/models/gemini-pro:generateContent`, embeddings, models list |

All providers include `/errors/sequential` and `/errors/weighted` variants with realistic error responses.

## Adding a New Provider

Create a JSON file in the `providers/` directory (or your custom specs dir):

```bash
providers/
  my-provider.json
```

Restart the server and the endpoints are immediately available.

## Server Endpoints

| Path | Description |
|------|-------------|
| `/_health` | Health check |
| `/_providers` | List loaded providers |
| `/_reset` | Reset all sequential counters |
| `/_version` | Server version |

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8080` | Port to listen on |
| `-specs` | `./providers` | Path to provider specs directory |
| `-debug` | `false` | Enable debug logging |
| `-version` | | Print version and exit |

## Development

```bash
make build
make test
make lint
```

## Docker

```bash
docker build -t mockllm .
docker run -p 8080:8080 mockllm
```

## License

MIT
