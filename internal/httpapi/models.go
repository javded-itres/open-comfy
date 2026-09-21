package httpapi

import (
	"net/http"
	"strconv"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	var data []map[string]any
	for _, m := range s.Cat.All() {
		data = append(data, modelOpenAI(&m))
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	id := catalog.NormalizeID(r.PathValue("id"))
	m, err := s.Cat.Resolve(id, "")
	if err != nil {
		writeError(w, 404, "invalid_request_error", "model_not_found", err.Error(), "model")
		return
	}
	writeJSON(w, 200, modelOpenAI(m))
}

func (s *Server) listImageModels(w http.ResponseWriter, r *http.Request) {
	var data []map[string]any
	for _, m := range s.Cat.All() {
		if m.Modality != "image" {
			continue
		}
		data = append(data, imageModelOR(&m))
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func (s *Server) listVideoModels(w http.ResponseWriter, r *http.Request) {
	var data []map[string]any
	for _, m := range s.Cat.All() {
		if m.Modality != "video" {
			continue
		}
		data = append(data, videoModelOR(&m))
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func modelOpenAI(m *catalog.Model) map[string]any {
	inMod := []string{"text"}
	outMod := []string{m.Modality}
	if m.Param("input_image") != nil {
		inMod = append(inMod, "image")
	}
	img := "0"
	if m.Pricing.PerImage > 0 {
		img = strconv.FormatFloat(m.Pricing.PerImage, 'f', -1, 64)
	}
	if m.Modality == "video" && m.Pricing.PerSecond > 0 {
		img = strconv.FormatFloat(m.Pricing.PerSecond, 'f', -1, 64)
	}
	return map[string]any{
		"id":       m.ID,
		"object":   "model",
		"created":  m.Created,
		"owned_by": "opencomfy",
		"name":     m.Name,
		"architecture": map[string]any{
			"input_modalities":  inMod,
			"output_modalities": outMod,
			"modality":          "text->" + m.Modality,
		},
		"pricing": map[string]any{
			"prompt": "0", "completion": "0", "request": "0",
			"image": img, "image_output": img,
		},
		"supported_parameters": m.SupportedNames(),
		"parameters":           m.PublicParams(),
		"required_parameters":  m.RequiredNames(),
		"input_schema":         m.InputSchema(),
		"mcp_tool":             m.ToolName(),
	}
}

func imageModelOR(m *catalog.Model) map[string]any {
	sp := map[string]any{}
	if p := m.Param("seed"); p != nil {
		sp["seed"] = map[string]any{"type": "boolean"}
	}
	if p := m.Param("n"); p != nil {
		min, max := 1.0, 1.0
		if p.Min != nil {
			min = *p.Min
		}
		if p.Max != nil {
			max = *p.Max
		}
		sp["n"] = map[string]any{"type": "range", "min": min, "max": max}
	}
	sp["size"] = map[string]any{"type": "string"}
	return map[string]any{
		"id":                   m.ID,
		"architecture":         map[string]any{"input_modalities": []string{"text"}, "output_modalities": []string{"image"}},
		"supported_parameters": sp,
		"supports_streaming":   false,
	}
}

func videoModelOR(m *catalog.Model) map[string]any {
	var durs []any
	if p := m.Param("seconds"); p != nil {
		durs = p.Enum
	}
	passthru := []string{}
	for _, p := range m.Parameters {
		if p.Name != "prompt" && p.Name != "seconds" {
			passthru = append(passthru, p.Name)
		}
	}
	sku := "0"
	if m.Pricing.PerSecond > 0 {
		sku = strconv.FormatFloat(m.Pricing.PerSecond, 'f', -1, 64)
	}
	return map[string]any{
		"id":                             m.ID,
		"name":                           m.Name,
		"supported_durations":            durs,
		"allowed_passthrough_parameters": passthru,
		"pricing_skus":                   map[string]any{"per-video-second": sku},
	}
}
