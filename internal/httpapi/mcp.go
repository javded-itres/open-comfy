package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

const (
	mcpProto2025   = "2025-06-18"
	mcpProto2025b  = "2025-03-26"
	mcpProto2024   = "2024-11-05"
	mcpProtoLatest = mcpProto2025
)

type mcpIn struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpOut struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpErr         `json:"error,omitempty"`
}

type mcpErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) mcpGET(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) mcpDELETE(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) mcpOPTIONS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "GET, POST, DELETE, OPTIONS")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mcpPOST(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(s.maxBody(r))
	if err != nil {
		s.writeMCP(w, r, mcpOut{JSONRPC: "2.0", Error: &mcpErr{Code: -32700, Message: "parse error"}}, http.StatusBadRequest)
		return
	}
	var in mcpIn
	if err := json.Unmarshal(body, &in); err != nil || in.Method == "" {
		s.writeMCP(w, r, mcpOut{JSONRPC: "2.0", Error: &mcpErr{Code: -32700, Message: "parse error"}}, http.StatusBadRequest)
		return
	}
	notify := len(bytes.TrimSpace(in.ID)) == 0 || string(in.ID) == "null"
	out, code := s.mcpDispatch(r, in)
	if notify {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if r.Header.Get("Mcp-Session-Id") == "" && in.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", mcpSessionID())
	} else if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	if v := mcpNegotiatedVersion(in); v != "" {
		w.Header().Set("MCP-Protocol-Version", v)
	}
	s.writeMCP(w, r, out, code)
}

func mcpNegotiatedVersion(in mcpIn) string {
	if in.Method != "initialize" {
		return ""
	}
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(in.Params, &p)
	return mcpPickProtocol(p.ProtocolVersion)
}

func (s *Server) mcpDispatch(r *http.Request, in mcpIn) (mcpOut, int) {
	out := mcpOut{JSONRPC: "2.0", ID: in.ID}
	switch in.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(in.Params, &p)
		out.Result = map[string]any{
			"protocolVersion": mcpPickProtocol(p.ProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{
				"name":    "opencomfy",
				"title":   "OpenComfy",
				"version": s.Cfg.Version,
			},
			"instructions": mcpInstructions,
		}
		return out, http.StatusOK
	case "notifications/initialized", "notifications/cancelled":
		return out, http.StatusAccepted
	case "ping":
		out.Result = map[string]any{}
		return out, http.StatusOK
	case "tools/list":
		out.Result = map[string]any{"tools": s.mcpTools()}
		return out, http.StatusOK
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if len(in.Params) > 0 {
			if err := json.Unmarshal(in.Params, &p); err != nil {
				out.Error = &mcpErr{Code: -32602, Message: "invalid params"}
				return out, http.StatusOK
			}
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		res, err := s.mcpCall(r, p.Name, p.Arguments)
		if err != nil {
			out.Result = mcpToolResult(map[string]any{"error": err.Error()}, true)
			return out, http.StatusOK
		}
		out.Result = mcpToolResult(res, false)
		return out, http.StatusOK
	case "resources/list":
		out.Result = map[string]any{"resources": []any{}}
		return out, http.StatusOK
	case "prompts/list":
		out.Result = map[string]any{"prompts": []any{}}
		return out, http.StatusOK
	default:
		out.Error = &mcpErr{Code: -32601, Message: "method not found"}
		return out, http.StatusOK
	}
}

func (s *Server) mcpTools() []map[string]any {
	str := map[string]any{"type": "string"}
	list := []map[string]any{
		{
			"name":        "list_models",
			"description": "List OpenComfy models (workflow maps). Each item has required_parameters, parameters, input_schema, and the generate_* tool name.",
			"inputSchema": mcpObjSchema(nil),
		},
		{
			"name":        "get_model",
			"description": "Full parameter schema for one model. Call this (or list_models) before generate if you are unsure which fields are required. input_image must be a data URL.",
			"inputSchema": mcpObjSchema(map[string]any{"model": str}, "model"),
		},
		{
			"name":        "get_video",
			"description": "Poll an async video job from generate_* until status is completed or failed. Completed JSON includes url.",
			"inputSchema": mcpObjSchema(map[string]any{"id": str}, "id"),
		},
	}
	for _, m := range s.Cat.All() {
		mm := m
		schema := mm.InputSchema()
		desc := fmt.Sprintf("Generate %s with model %s (%s). Required: %s. Images are data URLs (data:image/...;base64,...).",
			mm.Modality, mm.ID, mm.Name, strings.Join(mm.RequiredNames(), ", "))
		if mm.Modality == "video" {
			desc += " Returns a queued job; poll get_video."
		}
		list = append(list, map[string]any{
			"name":        mm.ToolName(),
			"description": desc,
			"inputSchema": schema,
		})
	}
	return list
}

func (s *Server) mcpCall(r *http.Request, name string, args map[string]any) (any, error) {
	switch name {
	case "list_models":
		var models []map[string]any
		for _, m := range s.Cat.All() {
			mm := m
			if !s.Auth.AllowModel(s.principal(r).Key, mm.ID) {
				continue
			}
			models = append(models, map[string]any{
				"id":                  mm.ID,
				"name":                mm.Name,
				"modality":            mm.Modality,
				"required_parameters": mm.RequiredNames(),
				"parameters":          mm.PublicParams(),
				"input_schema":        mm.InputSchema(),
				"tool":                mm.ToolName(),
			})
		}
		if models == nil {
			models = []map[string]any{}
		}
		return map[string]any{"models": models}, nil
	case "get_model":
		id, _ := args["model"].(string)
		if id == "" {
			id, _ = args["id"].(string)
		}
		m, err := s.Cat.Resolve(id, "")
		if err != nil {
			return nil, err
		}
		if !s.Auth.AllowModel(s.principal(r).Key, m.ID) {
			return nil, fmt.Errorf("model not allowed")
		}
		out := modelOpenAI(m)
		out["tool"] = m.ToolName()
		return out, nil
	case "get_video":
		id, _ := args["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("id required")
		}
		nr := httptest.NewRequest(http.MethodGet, "/v1/videos/"+id, nil).WithContext(r.Context())
		rec := httptest.NewRecorder()
		s.getVideo(rec, nr)
		if rec.Code >= 400 {
			return nil, fmt.Errorf("%s", rec.Body.String())
		}
		return mcpDecodeBody(rec)
	}
	if m := s.mcpModelForTool(name); m != nil {
		if !s.Auth.AllowModel(s.principal(r).Key, m.ID) {
			return nil, fmt.Errorf("model not allowed")
		}
		return s.mcpGenerate(r, m, args)
	}
	return nil, fmt.Errorf("unknown tool %s", name)
}

func (s *Server) mcpModelForTool(name string) *catalog.Model {
	for _, m := range s.Cat.All() {
		if m.ToolName() == name {
			got, err := s.Cat.Resolve(m.ID, "")
			if err != nil {
				return nil
			}
			return got
		}
	}
	return nil
}

func (s *Server) mcpGenerate(r *http.Request, m *catalog.Model, args map[string]any) (any, error) {
	args["model"] = m.ID
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	path := "/v1/images/generations"
	if m.Modality == "video" {
		path = "/v1/videos"
	}
	nr := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)).WithContext(r.Context())
	nr.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if m.Modality == "video" {
		s.createVideo(rec, nr)
	} else {
		s.images(rec, nr)
	}
	out, err := mcpDecodeBody(rec)
	if err != nil {
		return nil, err
	}
	if rec.Code >= 400 {
		return nil, fmt.Errorf("%s", rec.Body.String())
	}
	return out, nil
}

func mcpDecodeBody(rec *httptest.ResponseRecorder) (any, error) {
	var out any
	if rec.Body.Len() == 0 {
		return map[string]any{"status": rec.Code}, nil
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return map[string]any{"status": rec.Code, "body": rec.Body.String()}, nil
	}
	return out, nil
}

func mcpObjSchema(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func mcpToolResult(v any, isErr bool) map[string]any {
	raw, _ := json.Marshal(v)
	out := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": string(raw)},
		},
		"structuredContent": v,
	}
	if isErr {
		out["isError"] = true
	}
	return out
}

func (s *Server) writeMCP(w http.ResponseWriter, r *http.Request, out mcpOut, code int) {
	raw, err := json.Marshal(out)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	accept := r.Header.Get("Accept")
	wantSSE := strings.Contains(accept, "text/event-stream") && !strings.Contains(accept, "application/json")
	if wantSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(code)
		_, _ = w.Write([]byte("event: message\ndata: "))
		_, _ = w.Write(raw)
		_, _ = w.Write([]byte("\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(raw)
	_, _ = w.Write([]byte("\n"))
}

func mcpPickProtocol(client string) string {
	switch strings.TrimSpace(client) {
	case mcpProto2024, mcpProto2025b, mcpProto2025, "2025-11-25":
		return client
	default:
		return mcpProtoLatest
	}
}

func mcpSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const mcpInstructions = `OpenComfy maps named models to ComfyUI workflows. Call list_models (or get_model) to see required_parameters and the matching generate_* tool. Required image fields (input_image) must be data URLs (data:image/png;base64,...); aliases input_images / input_reference / input_references are accepted. Video generate_* returns a queued job — poll get_video until status=completed (includes url).`
