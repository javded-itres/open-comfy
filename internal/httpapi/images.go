package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/javded-itres/open-comfy/internal/engine"
	"github.com/javded-itres/open-comfy/internal/ids"
	"github.com/javded-itres/open-comfy/internal/queue"
	"github.com/javded-itres/open-comfy/internal/workflow"
)

type syncGenKey struct{}

func withSyncGen(ctx context.Context) context.Context {
	return context.WithValue(ctx, syncGenKey{}, true)
}

func forceSync(r *http.Request) bool {
	v, _ := r.Context().Value(syncGenKey{}).(bool)
	return v
}

func (s *Server) images(w http.ResponseWriter, r *http.Request) {
	p := s.principal(r)
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
	m, err := s.Cat.Resolve(modelID, "image")
	if err != nil {
		writeError(w, 400, "invalid_request_error", "model_not_found", err.Error(), "model")
		return
	}
	if !s.allowModel(w, p, m) {
		return
	}
	req, err := parseImageReq(raw)
	if err != nil {
		mapErr(w, err)
		return
	}
	if req.ResponseFormat == "" {
		if s.Cfg.PublicBaseURL == "" {
			req.ResponseFormat = "b64_json"
		} else {
			req.ResponseFormat = "url"
		}
	}
	n := req.N
	if n <= 0 {
		n = 1
	}
	if np := m.Param("n"); np != nil && np.Max != nil && float64(n) > *np.Max {
		writeError(w, 400, "invalid_request_error", "invalid_value", "n exceeds max", "n")
		return
	}
	req.N = n

	sync := forceSync(r)
	if !sync && s.generationBusy(r.Context()) {
		s.enqueueImage(w, r, p, m, req)
		return
	}

	if !s.Auth.AcquireKeySlot(p.Key.Hash, p.Key.MaxConcurrent) {
		w.Header().Set("Retry-After", "5")
		writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "too many concurrent requests", "")
		return
	}
	defer s.Auth.ReleaseKeySlot(p.Key.Hash)

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(m.TimeoutS+15)*time.Second)
	defer cancel()
	rel, ok := s.Admit.TryAcquire(m.ID, m.MaxConcurrent)
	if !ok {
		if !sync {
			s.enqueueImage(w, r, p, m, req)
			return
		}
		var err error
		rel, err = s.Admit.Acquire(ctx, m.ID, m.MaxConcurrent)
		if errors.Is(err, queue.ErrBusy) {
			w.Header().Set("Retry-After", "10")
			writeError(w, 429, "rate_limit_error", "rate_limit_exceeded", "gpu busy", "")
			return
		}
		if err != nil {
			writeError(w, 504, "timeout", "generation_timeout", "timeout waiting for slot", "")
			return
		}
	}
	defer rel()

	var data []map[string]any
	baseSeed := req.Seed
	for i := 0; i < n; i++ {
		one := req
		if baseSeed != nil {
			s := *baseSeed + int64(i)
			one.Seed = &s
		} else {
			one.Seed = nil
		}
		res, _, err := engine.Run(ctx, s.Comfy, s.Cat, m, one, ids.New("img_"), nil)
		if err != nil {
			if ctx.Err() != nil {
				writeError(w, 504, "timeout", "generation_timeout", "generation timed out", "")
				return
			}
			mapErr(w, err)
			return
		}
		item := map[string]any{"revised_prompt": nil}
		if req.ResponseFormat == "url" {
			meta, err := s.Files.Put(p.Key.Hash, res.MIME, res.Bytes)
			if err != nil {
				writeError(w, 507, "api_error", "storage_full", err.Error(), "")
				return
			}
			u, err := s.Files.SignURL(meta.ID)
			if err != nil {
				writeError(w, 400, "invalid_request_error", "invalid_value", "cannot emit url: "+err.Error(), "response_format")
				return
			}
			item["url"] = u
		} else {
			item["b64_json"] = base64.StdEncoding.EncodeToString(res.Bytes)
		}
		data = append(data, item)
	}
	cost := m.Pricing.PerImage * float64(n)
	writeJSON(w, 200, map[string]any{
		"created":       time.Now().Unix(),
		"data":          data,
		"size":          workflow.SizeString(map[string]any{}),
		"output_format": mimeExt(m.OutputMIME),
		"usage": map[string]any{
			"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0, "cost": cost,
		},
	})
}

func mimeExt(m string) string {
	switch m {
	case "image/jpeg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func parseImageReq(raw map[string]any) (workflow.Request, error) {
	req := workflow.Request{Extra: map[string]any{}}
	req.Model, _ = raw["model"].(string)
	req.Prompt, _ = raw["prompt"].(string)
	req.Prompt = workflow.CleanChatPrompt(req.Prompt)
	req.NegativePrompt, _ = raw["negative_prompt"].(string)
	req.Size, _ = raw["size"].(string)
	req.Quality, _ = raw["quality"].(string)
	req.ResponseFormat, _ = raw["response_format"].(string)
	req.Resolution, _ = raw["resolution"].(string)
	req.AspectRatio, _ = raw["aspect_ratio"].(string)
	if v, ok := raw["n"].(float64); ok {
		req.N = int(v)
	}
	if v, ok := asIntPtr(raw["width"]); ok {
		req.Width = v
	}
	if v, ok := asIntPtr(raw["height"]); ok {
		req.Height = v
	}
	if v, ok := asInt64Ptr(raw["seed"]); ok {
		req.Seed = v
	}
	if img, ok := raw["input_image"]; ok {
		if err := applyInputReference(&req, img); err != nil {
			return req, err
		}
	}
	if imgs, ok := raw["input_images"]; ok {
		if err := applyInputReference(&req, imgs); err != nil {
			return req, err
		}
	}
	if ir, ok := raw["input_reference"]; ok {
		if err := applyInputReference(&req, ir); err != nil {
			return req, err
		}
	}
	if ir, ok := raw["input_references"]; ok {
		if err := applyInputReference(&req, ir); err != nil {
			return req, err
		}
	}
	known := map[string]bool{}
	for k := range raw {
		if workflow.IgnoreOpenAI(k) {
			continue
		}
		req.Extra[k] = raw[k]
		known[k] = true
	}
	_ = known
	return req, nil
}

func applyInputReference(req *workflow.Request, ir any) error {
	switch t := ir.(type) {
	case []any:
		for _, it := range t {
			if err := applyInputReference(req, it); err != nil {
				return err
			}
		}
		return nil
	case string:
		if strings.HasPrefix(t, "data:") {
			b, err := engine.DecodeDataURL(t)
			if err != nil {
				return &workflow.Error{Code: "invalid_value", Param: "input_reference", Message: err.Error()}
			}
			appendRef(req, b)
			return nil
		}
		if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
			return &workflow.Error{Code: "invalid_value", Param: "input_reference", Message: "remote images disabled"}
		}
		return &workflow.Error{Code: "not_supported", Param: "input_reference", Message: "file_id is not supported in v1"}
	case map[string]any:
		if _, ok := t["file_id"]; ok {
			return &workflow.Error{Code: "not_supported", Param: "input_reference", Message: "file_id is not supported in v1"}
		}
		if u, ok := t["image_url"].(string); ok {
			return applyInputReference(req, u)
		}
		if m, ok := t["image_url"].(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				return applyInputReference(req, u)
			}
		}
	}
	return &workflow.Error{Code: "invalid_value", Param: "input_reference", Message: "invalid input_reference"}
}

func appendRef(req *workflow.Request, b []byte) {
	req.InputImages = append(req.InputImages, b)
	if len(req.InputImage) == 0 {
		req.InputImage = b
	}
	req.HasInputImage = true
}

func asIntPtr(v any) (*int, bool) {
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n, true
	case int:
		return &t, true
	}
	return nil, false
}

func asInt64Ptr(v any) (*int64, bool) {
	switch t := v.(type) {
	case float64:
		n := int64(t)
		return &n, true
	case int:
		n := int64(t)
		return &n, true
	case int64:
		return &t, true
	}
	return nil, false
}
