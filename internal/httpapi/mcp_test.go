package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mcpRPC(t *testing.T, h http.Handler, key, method string, params any) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func mcpCall(t *testing.T, h http.Handler, key, name string, args map[string]any) map[string]any {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	code, out := mcpRPC(t, h, key, "tools/call", map[string]any{"name": name, "arguments": args})
	if code != 200 {
		t.Fatalf("%s http %d %+v", name, code, out)
	}
	if errObj, ok := out["error"].(map[string]any); ok {
		t.Fatalf("%s rpc error %+v", name, errObj)
	}
	res, _ := out["result"].(map[string]any)
	if res == nil {
		t.Fatalf("%s no result %+v", name, out)
	}
	sc, _ := res["structuredContent"].(map[string]any)
	if sc == nil {
		t.Fatalf("%s no structuredContent %+v", name, res)
	}
	return sc
}

func TestMCPListAndSchema(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()

	code, out := mcpRPC(t, h, key, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	if code != 200 {
		t.Fatalf("init %d %+v", code, out)
	}
	res, _ := out["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" {
		t.Fatalf("proto %+v", res)
	}

	code, out = mcpRPC(t, h, key, "tools/list", nil)
	if code != 200 {
		t.Fatalf("list %d %+v", code, out)
	}
	res, _ = out["result"].(map[string]any)
	tools, _ := res["tools"].([]any)
	var ltx map[string]any
	for _, it := range tools {
		tm, _ := it.(map[string]any)
		if tm["name"] == "generate_ltx2-i2v" {
			ltx = tm
		}
	}
	if ltx == nil {
		t.Fatalf("missing generate_ltx2-i2v in %+v", tools)
	}
	schema, _ := ltx["inputSchema"].(map[string]any)
	reqd, _ := schema["required"].([]any)
	joined := fmtAny(reqd)
	if !strings.Contains(joined, "input_image") || !strings.Contains(joined, "prompt") {
		t.Fatalf("required %v", reqd)
	}

	sc := mcpCall(t, h, key, "get_model", map[string]any{"model": "ltx2-i2v"})
	reqNames, _ := sc["required_parameters"].([]any)
	if !strings.Contains(fmtAny(reqNames), "input_image") {
		t.Fatalf("get_model required %+v", sc["required_parameters"])
	}
}

func TestMCPGenerateRequiresImage(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()

	code, out := mcpRPC(t, h, key, "tools/call", map[string]any{
		"name":      "generate_ltx2-i2v",
		"arguments": map[string]any{"prompt": "walk"},
	})
	if code != 200 {
		t.Fatalf("http %d %+v", code, out)
	}
	res, _ := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError %+v", res)
	}
	text, _ := json.Marshal(res)
	if !strings.Contains(string(text), "input_image") {
		t.Fatalf("want missing input_image: %s", text)
	}

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1)
	sc := mcpCall(t, h, key, "generate_ltx2-i2v", map[string]any{
		"prompt":           "walk",
		"input_references": []any{dataURL},
	})
	if sc["id"] == nil && sc["status"] == nil {
		t.Fatalf("queued job %+v", sc)
	}
}

func TestMCPGenerateImage(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	h := s.Handler()
	sc := mcpCall(t, h, key, "generate_toy-image", map[string]any{"prompt": "a cube"})
	data, _ := sc["data"].([]any)
	if len(data) == 0 {
		t.Fatalf("%+v", sc)
	}
}

func TestModelCatalogExposesRequired(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	req := httptest.NewRequest(http.MethodGet, "/v1/models/ltx2-i2v", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	reqd, _ := m["required_parameters"].([]any)
	if !strings.Contains(fmtAny(reqd), "input_image") {
		t.Fatalf("%s", rec.Body.String())
	}
	if m["mcp_tool"] != "generate_ltx2-i2v" {
		t.Fatalf("tool %v", m["mcp_tool"])
	}
}

func TestVideoMissingInputImage(t *testing.T) {
	cu := mockComfy(t)
	defer cu.Close()
	s, key := testServer(t, cu.URL)
	body := `{"model":"ltx2-i2v","prompt":"walk"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "input_image") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}

	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png1)
	okBody, _ := json.Marshal(map[string]any{
		"model":            "ltx2-i2v",
		"prompt":           "walk",
		"input_references": []any{dataURL},
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(okBody))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("with refs %d %s", rec.Code, rec.Body.String())
	}
}

func fmtAny(v []any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
