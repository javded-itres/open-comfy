package analyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/javded-itres/open-comfy/internal/comfy"
)

func TestAnalyzeReusesLocalBasename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "checkpoints", "missing.safetensors")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := map[string]comfy.NodeDef{
		"CLIPLoader": {
			Input: map[string]map[string]any{
				"required": {
					"clip_name": []any{[]any{"qwen/qwen_3_4b.safetensors"}},
				},
			},
		},
	}
	ui := json.RawMessage(`{
	  "nodes": [
	    {"id": 1, "type": "CLIPLoader", "inputs": [{"name": "clip_name", "widget": {"name": "clip_name"}}],
	      "widgets_values_named": {"clip_name": "missing.safetensors"}}
	  ],
	  "links": []
	}`)
	a := Analyze(ui, info, nil, dir)
	if !a.Ready || len(a.MissingModels) != 0 {
		t.Fatalf("should reuse local: %+v", a)
	}
	if len(a.ReusedModels) != 1 || a.ReusedModels[0].Value != "missing.safetensors" {
		t.Fatalf("reused %+v", a.ReusedModels)
	}
	dest := filepath.Join(dir, "text_encoders", "missing.safetensors")
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "clip" {
		t.Fatalf("%q", b)
	}
}
