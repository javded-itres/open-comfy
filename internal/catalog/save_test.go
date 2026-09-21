package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSkipsInvalidModel(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "ok.json")
	body := []byte(`{"9":{"class_type":"SaveImage","inputs":{"images":["1",0],"filename_prefix":"x"}},"6":{"class_type":"CLIPTextEncode","inputs":{"text":"hi","clip":["1",0]}}}`)
	if err := os.WriteFile(wf, body, 0o644); err != nil {
		t.Fatal(err)
	}
	yml := filepath.Join(dir, "models.yaml")
	src := []byte("models:\n" +
		"  - id: ok\n    workflow: ok.json\n    modality: image\n    output_node: \"9\"\n    parameters:\n" +
		"      - name: prompt\n        maps_to:\n          - node: \"6\"\n            field: text\n" +
		"  - id: broken\n    workflow: missing.json\n    modality: image\n")
	if err := os.WriteFile(yml, src, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(yml, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("ok") || c.Has("broken") {
		t.Fatalf("ok=%v broken=%v n=%d", c.Has("ok"), c.Has("broken"), len(c.Models))
	}
}

func TestEnsureSaverAddsSaveImage(t *testing.T) {
	g := map[string]any{
		"5962": map[string]any{
			"class_type": "FluxKleinOneNode",
			"inputs":     map[string]any{"prompt": "hi"},
		},
	}
	out, pick := EnsureSaver(g, "image", "5962")
	if pick != "oc_save" {
		t.Fatalf("pick %s", pick)
	}
	node := out[pick].(map[string]any)
	if node["class_type"] != "SaveImage" {
		t.Fatalf("%v", node["class_type"])
	}
	imgs := node["inputs"].(map[string]any)["images"].([]any)
	if imgs[0] != "5962" {
		t.Fatalf("%v", imgs)
	}
	if _, ok := g["oc_save"]; ok {
		t.Fatal("must not mutate original")
	}
}

func TestRemoveModel(t *testing.T) {
	c := &Catalog{
		DefaultImage: "a",
		Models: []Model{
			{ID: "a", Workflow: "a.json"},
			{ID: "b", Workflow: "b.json"},
		},
	}
	c.reindex()
	m, ok := c.Remove("a")
	if !ok || m.ID != "a" || c.Has("a") || !c.Has("b") || c.DefaultImage != "" {
		t.Fatalf("%+v hasA=%v hasB=%v def=%q", m, c.Has("a"), c.Has("b"), c.DefaultImage)
	}
}

func TestInputSchemaRequiresImage(t *testing.T) {
	m := Model{
		ID: "ltx2-i2v",
		Parameters: []Param{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "input_image", Type: "image", Required: true},
		},
	}
	if m.ToolName() != "generate_ltx2-i2v" {
		t.Fatal(m.ToolName())
	}
	s := m.InputSchema()
	req, _ := s["required"].([]string)
	if len(req) != 2 || req[0] != "prompt" || req[1] != "input_image" {
		t.Fatalf("%v", req)
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["input_images"]; !ok {
		t.Fatal("input_images alias")
	}
}

func TestEnsureSaverKeepsExisting(t *testing.T) {
	g := map[string]any{
		"9": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"filename_prefix": "x"}},
	}
	_, pick := EnsureSaver(g, "image", "9")
	if pick != "9" {
		t.Fatal(pick)
	}
}
