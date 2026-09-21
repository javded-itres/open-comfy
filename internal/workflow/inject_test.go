package workflow

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

func TestCleanChatPrompt(t *testing.T) {
	plain := "гнилое яблоко"
	if CleanChatPrompt(plain) != plain {
		t.Fatal("plain")
	}
	folded := "Original image task and subsequent revisions. Produce one image matching the latest state.\n\nUser: Сгенерируй яблоко\nAssistant: prompt 400: {\"error\":{\"type\":\"value_not_in_list\"}}\nUser: гнилое яблоко"
	if got := CleanChatPrompt(folded); got != "гнилое яблоко" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildValuesDropsFoldedChatPrompt(t *testing.T) {
	cat := loadCat(t)
	m, err := cat.Resolve("toy-image", "image")
	if err != nil {
		t.Fatal(err)
	}
	folded := "Original image task and subsequent revisions. Produce one image matching the latest state.\n\nUser: Одна девушка в полный рост"
	vals, err := BuildValues(cat, m, Request{
		Prompt: CleanChatPrompt(folded),
		Extra:  map[string]any{"prompt": folded},
	})
	if err != nil {
		t.Fatal(err)
	}
	if vals["prompt"] != "Одна девушка в полный рост" {
		t.Fatalf("prompt=%v", vals["prompt"])
	}
}

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

func TestInjectMultipleImages(t *testing.T) {
	g := map[string]any{
		"1": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": ""}},
		"2": map[string]any{"class_type": "LoadImage", "inputs": map[string]any{"image": ""}},
	}
	m := &catalog.Model{Parameters: []catalog.Param{{
		Name: "input_image", Type: "image",
		MapsTo: []catalog.MapTo{
			{Node: "1", Field: "image"},
			{Node: "2", Field: "image"},
		},
	}}}
	out, err := Inject(g, m, map[string]any{"input_image": []string{"a.png", "b.png"}})
	if err != nil {
		t.Fatal(err)
	}
	a := out["1"].(map[string]any)["inputs"].(map[string]any)["image"]
	b := out["2"].(map[string]any)["inputs"].(map[string]any)["image"]
	if a != "a.png" || b != "b.png" {
		t.Fatalf("%v %v", a, b)
	}
}

func TestStripOpenAIPrefix(t *testing.T) {
	if catalog.NormalizeID("openai/flux-dev") != "flux-dev" {
		t.Fatal(catalog.NormalizeID("openai/flux-dev"))
	}
}
