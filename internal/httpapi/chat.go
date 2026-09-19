package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.HTTP.ChatShim {
		writeError(w, 404, "invalid_request_error", "not_found", "chat shim disabled", "")
		return
	}
	body, err := io.ReadAll(s.maxBody(r))
	if err != nil {
		writeError(w, 413, "invalid_request_error", "payload_too_large", "payload too large", "")
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if v, _ := raw["stream"].(bool); v {
		writeError(w, 400, "invalid_request_error", "not_supported", "stream is not supported", "stream")
		return
	}
	modelID, _ := raw["model"].(string)
	m, err := s.Cat.Resolve(modelID, "")
	if err != nil {
		writeError(w, 400, "invalid_request_error", "model_not_found", err.Error(), "model")
		return
	}
	if m.Modality == "video" {
		writeError(w, 400, "invalid_request_error", "invalid_value", "video models use POST /v1/videos", "model")
		return
	}
	prompt := extractChatPrompt(raw)
	gen := map[string]any{
		"model":  m.ID,
		"prompt": prompt,
	}
	for _, k := range []string{"size", "quality", "n", "seed", "response_format", "negative_prompt"} {
		if v, ok := raw[k]; ok {
			gen[k] = v
		}
	}
	b, _ := json.Marshal(gen)
	nr := r.Clone(r.Context())
	nr.Body = io.NopCloser(bytes.NewReader(b))
	nr.Header.Set("Content-Type", "application/json")
	cw := &capture{ResponseWriter: w, code: 200, buf: &bytes.Buffer{}}
	s.images(cw, nr)
	if cw.code != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cw.code)
		_, _ = w.Write(cw.buf.Bytes())
		return
	}
	var img struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	_ = json.Unmarshal(cw.buf.Bytes(), &img)
	b64 := ""
	url := ""
	if len(img.Data) > 0 {
		b64 = img.Data[0].B64JSON
		url = img.Data[0].URL
	}
	dataURL := url
	if b64 != "" {
		dataURL = "data:image/png;base64," + b64
	}
	writeJSON(w, 200, map[string]any{
		"id":      "chatcmpl-opencomfy",
		"object":  "chat.completion",
		"created": img.Created,
		"model":   m.ID,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": "",
				"images":  []map[string]any{{"image_url": map[string]any{"url": dataURL}}},
			},
			"finish_reason": "stop",
		}},
		"data": []map[string]any{{"b64_json": b64, "url": url}},
	})
}

type capture struct {
	http.ResponseWriter
	code int
	buf  *bytes.Buffer
}

func (c *capture) WriteHeader(code int) { c.code = code }
func (c *capture) Write(b []byte) (int, error) {
	return c.buf.Write(b)
}

func extractChatPrompt(raw map[string]any) string {
	msgs, _ := raw["messages"].([]any)
	var last string
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		c := mm["content"]
		switch t := c.(type) {
		case string:
			last = t
		case []any:
			for _, p := range t {
				pm, _ := p.(map[string]any)
				if pm["type"] == "text" {
					if s, ok := pm["text"].(string); ok {
						last = s
					}
				}
			}
		}
	}
	return last
}
