package catalogimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

func TestGatePromptAndReference(t *testing.T) {
	m := catalog.Model{}
	graph := map[string]any{
		"1": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": "a.png"}},
	}
	errs := Gate(&m, graph)
	if len(errs) < 2 {
		t.Fatalf("%v", errs)
	}
	g2 := map[string]any{
		"6": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}},
		"9": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"filename_prefix": "x"}},
	}
	m2, err := Infer("t2i", "t2i.json", "t2i", g2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Param("prompt") == nil {
		t.Fatal("prompt")
	}
	g3 := map[string]any{
		"6": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hi"}},
		"1": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": "a.png"}},
		"9": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"filename_prefix": "x"}},
	}
	m3, err := Infer("i2i", "i2i.json", "i2i", g3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m3.Param("input_image") == nil {
		t.Fatal("reference")
	}
}

func TestUnloadRemovesYamlNotComfy(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, "workflows")
	os.MkdirAll(wf, 0o755)
	os.WriteFile(filepath.Join(wf, "gone.json"), []byte(`{"9":{"class_type":"SaveImage","inputs":{"filename_prefix":"x","images":["8",0]}}}`), 0o644)
	models := filepath.Join(dir, "models.yaml")
	os.WriteFile(models, []byte("models:\n  - id: gone\n    modality: image\n    workflow: gone.json\n    output_node: \"9\"\n"), 0o644)
	res, err := Unload(models, wf, []string{"gone"})
	if err != nil || len(res.Removed) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(wf, "gone.json")); !os.IsNotExist(err) {
		t.Fatal("copy should be deleted")
	}
	b, _ := os.ReadFile(models)
	if strings.Contains(string(b), "id: gone") {
		t.Fatalf("%s", b)
	}
}

func TestSlug(t *testing.T) {
	if g := Slug("MiniMax H3 Talking Avatar.json"); g != "minimax-h3-talking-avatar" {
		t.Fatal(g)
	}
}
