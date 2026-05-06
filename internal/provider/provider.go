package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

type Response struct {
	Status       int               `json:"status"`
	Body         json.RawMessage   `json:"body"`
	Stream       bool              `json:"stream,omitempty"`
	StreamChunks []StreamChunk     `json:"stream_chunks,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Delay        string            `json:"delay,omitempty"`
	Weight       int               `json:"weight,omitempty"`
	Label        string            `json:"label,omitempty"`
}

func (r *Response) isStream() bool {
	return r.Stream || len(r.StreamChunks) > 0
}

type StreamChunk struct {
	Data  json.RawMessage `json:"data"`
	Delay string          `json:"delay,omitempty"`
}

type Endpoint struct {
	Path      string     `json:"path"`
	Method    string     `json:"method"`
	Responses []Response `json:"responses"`
	MatchMode string     `json:"match_mode,omitempty"`
}

type Provider struct {
	Name      string     `json:"name"`
	Version   string     `json:"version"`
	BasePath  string     `json:"base_path"`
	Endpoints []Endpoint `json:"endpoints"`
}

type endpointMeta struct {
	responses       []Response
	streamResponses []Response
	mode            string
	counter         atomic.Uint64
}

type Registry struct {
	mu        sync.RWMutex
	providers map[string]*Provider
	routes    map[string]*endpointMeta
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]*Provider),
		routes:    make(map[string]*endpointMeta),
	}
}

func routeKey(method, path string) string {
	return strings.ToUpper(method) + ":" + path
}

func (r *Registry) LoadDir(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}
		if err := r.LoadFile(path); err != nil {
			return fmt.Errorf("loading %s: %w", path, err)
		}
		return nil
	})
}

func (r *Registry) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	var p Provider
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	if err := validateProvider(&p); err != nil {
		return fmt.Errorf("validating %s: %w", path, err)
	}

	compactResponses(&p)

	r.Register(&p)
	return nil
}

func validateProvider(p *Provider) error {
	if p.Name == "" {
		return fmt.Errorf("provider name is required")
	}
	for i, ep := range p.Endpoints {
		if ep.Path == "" {
			return fmt.Errorf("endpoint %d: path is required", i)
		}
		if ep.Method == "" {
			return fmt.Errorf("endpoint %d (%s): method is required", i, ep.Path)
		}
		if len(ep.Responses) == 0 {
			return fmt.Errorf("endpoint %d (%s %s): at least one response is required", i, ep.Method, ep.Path)
		}
		for j, resp := range ep.Responses {
			if resp.Status < 100 || resp.Status > 599 {
				return fmt.Errorf("endpoint %d (%s %s), response %d: invalid status code %d", i, ep.Method, ep.Path, j, resp.Status)
			}
		}
		if ep.MatchMode == "weighted" {
			hasWeight := false
			for _, resp := range ep.Responses {
				if resp.Weight > 0 {
					hasWeight = true
					break
				}
			}
			if !hasWeight {
				return fmt.Errorf("endpoint %d (%s %s): weighted mode requires at least one response with weight > 0", i, ep.Method, ep.Path)
			}
		}
	}
	return nil
}

func compactResponses(p *Provider) {
	for i := range p.Endpoints {
		for j := range p.Endpoints[i].Responses {
			resp := &p.Endpoints[i].Responses[j]
			resp.Body = compactJSON(resp.Body)
			for k := range resp.StreamChunks {
				resp.StreamChunks[k].Data = compactJSON(resp.StreamChunks[k].Data)
			}
		}
	}
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		return buf.Bytes()
	}
	return raw
}

func (r *Registry) Register(p *Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers[p.Name] = p

	for i := range p.Endpoints {
		ep := &p.Endpoints[i]
		fullPath := p.BasePath + ep.Path
		key := routeKey(ep.Method, fullPath)

		var streamResps []Response
		for _, resp := range ep.Responses {
			if resp.isStream() {
				streamResps = append(streamResps, resp)
			}
		}

		r.routes[key] = &endpointMeta{
			responses:       ep.Responses,
			streamResponses: streamResps,
			mode:            ep.MatchMode,
		}
	}
}

func (r *Registry) Match(method, path string, wantsStream bool) (*Provider, *Response, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name := range r.providers {
		key := routeKey(method, path)
		meta, ok := r.routes[key]
		if !ok {
			continue
		}

		resp := r.selectResponse(meta, wantsStream)
		return r.providers[name], resp, true
	}
	return nil, nil, false
}

func (r *Registry) selectResponse(meta *endpointMeta, wantsStream bool) *Response {
	if wantsStream && len(meta.streamResponses) > 0 {
		return &meta.streamResponses[0]
	}

	switch meta.mode {
	case "sequential":
		return r.selectSequential(meta)
	case "weighted":
		return r.selectWeighted(meta)
	default:
		for i := range meta.responses {
			if !meta.responses[i].isStream() {
				return &meta.responses[i]
			}
		}
		return &meta.responses[0]
	}
}

func (r *Registry) selectSequential(meta *endpointMeta) *Response {
	idx := meta.counter.Add(1) - 1
	return &meta.responses[idx%uint64(len(meta.responses))]
}

func (r *Registry) selectWeighted(meta *endpointMeta) *Response {
	total := 0
	for _, c := range meta.responses {
		total += c.Weight
	}
	if total == 0 {
		return &meta.responses[0]
	}

	roll := rand.IntN(total)

	running := 0
	for i := range meta.responses {
		running += meta.responses[i].Weight
		if roll < running {
			return &meta.responses[i]
		}
	}
	return &meta.responses[0]
}

func (r *Registry) Providers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Reset() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, meta := range r.routes {
		meta.counter.Store(0)
	}
}
