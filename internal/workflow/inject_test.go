package workflow

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

func testdata() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

func loadCat(t *testing.T) *catalog.Catalog {
	t.Helper()
	td := testdata()
	c, err := catalog.Load(filepath.Join(td, "models.yaml"), filepath.Join(td, "workflows"), false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestInjectFlux2(t *testing.T) {
	cat := loadCat(t)
	m, err := cat.Resolve("flux-dev", "image")
	if err != nil {
		t.Fatal(err)
	}
	g, err := catalog.LoadGraph(cat.WorkflowsDir, m.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(42)
	vals, err := BuildValues(cat, m, Request{Prompt: "red cube", Seed: &seed, Size: "1024x1024"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Inject(g, m, vals)
	if err != nil {
		t.Fatal(err)
	}
	n76 := out["76"].(map[string]any)["inputs"].(map[string]any)
	if n76["value"] != "red cube" {
		t.Fatalf("prompt=%v", n76["value"])
	}
	n73 := out["75:73"].(map[string]any)["inputs"].(map[string]any)
	if n73["noise_seed"] != int64(42) && n73["noise_seed"] != 42 {
		t.Fatalf("seed=%v %T", n73["noise_seed"], n73["noise_seed"])
	}
}

func TestInjectNestedPath(t *testing.T) {
	cat := loadCat(t)
	m, err := cat.Resolve("nested-resize", "image")
	if err != nil {
		t.Fatal(err)
	}
	g, err := catalog.LoadGraph(cat.WorkflowsDir, m.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	w := 128
	vals, err := BuildValues(cat, m, Request{Width: &w, Height: &w})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Inject(g, m, vals)
	if err != nil {
		t.Fatal(err)
	}
	rt := out["10"].(map[string]any)["inputs"].(map[string]any)["resize_type"].(map[string]any)
	if rt["width"] != 128 {
		t.Fatalf("width=%v", rt["width"])
	}
}

func TestInjectLTX2(t *testing.T) {
	cat := loadCat(t)
	m, err := cat.Resolve("ltx2-i2v", "video")
	if err != nil {
		t.Fatal(err)
	}
	g, err := catalog.LoadGraph(cat.WorkflowsDir, m.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := BuildValues(cat, m, Request{Prompt: "walk", HasInputImage: true, InputName: "x.png", Seconds: 6})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Inject(g, m, vals)
	if err != nil {
		t.Fatal(err)
	}
	img := out["5180"].(map[string]any)["inputs"].(map[string]any)["image"]
	if img != "x.png" {
		t.Fatalf("image=%v", img)
	}
}

func TestStripOpenAIPrefix(t *testing.T) {
	if catalog.NormalizeID("openai/flux-dev") != "flux-dev" {
		t.Fatal(catalog.NormalizeID("openai/flux-dev"))
	}
}
