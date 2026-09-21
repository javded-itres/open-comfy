package importwf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
)

func TestConvertRealZImageTurbo(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdataDir(), "workflows", "image_z_image_turbo.ui.json"))
	if err != nil {
		t.Skip(err)
	}
	g, err := Convert(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := catalog.FirstUUIDClass(g); n != "" {
		t.Fatalf("still UUID node %s in %v", n, keysOf(g))
	}
	enc, ok := g["57:27"].(map[string]any)
	if !ok || enc["class_type"] != "CLIPTextEncode" {
		t.Fatalf("missing expanded CLIPTextEncode 57:27 in %v", keysOf(g))
	}
	text, _ := enc["inputs"].(map[string]any)["text"].(string)
	if text == "" {
		t.Fatal("empty prompt")
	}
	img := g["9"].(map[string]any)["inputs"].(map[string]any)["images"]
	link, ok := img.([]any)
	if !ok || nodeIDString(link[0]) != "57:8" {
		t.Fatalf("SaveImage images=%v", img)
	}
	ks := g["57:3"].(map[string]any)["inputs"].(map[string]any)
	if fmtNum(ks["steps"]) != 8 {
		t.Fatalf("steps=%v (control_after_generate leak?) %v", ks["steps"], ks)
	}
	if ks["sampler_name"] != "res_multistep" {
		t.Fatalf("sampler=%v", ks["sampler_name"])
	}
	clip := g["57:30"].(map[string]any)["inputs"].(map[string]any)["clip_name"]
	if clip != "qwen/qwen_3_4b.safetensors" {
		t.Fatalf("clip_name=%v want folder prefix", clip)
	}
	unet := g["57:28"].(map[string]any)["inputs"].(map[string]any)["unet_name"]
	if unet != "ZIT/z_image_turbo_bf16.safetensors" {
		t.Fatalf("unet_name=%v want folder prefix", unet)
	}
}

func TestOverlayPromotedComboFromComfyConvert(t *testing.T) {
	ui, err := os.ReadFile(filepath.Join(testdataDir(), "workflows", "image_z_image_turbo.ui.json"))
	if err != nil {
		t.Skip(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "57:30":{"class_type":"CLIPLoader","inputs":{"clip_name":"qwen_3_4b.safetensors","type":"lumina2","device":"default"}},
		  "57:28":{"class_type":"UNETLoader","inputs":{"unet_name":"z_image_turbo_bf16.safetensors","weight_dtype":"default"}},
		  "57:29":{"class_type":"VAELoader","inputs":{"vae_name":"ae.safetensors"}},
		  "57:27":{"class_type":"CLIPTextEncode","inputs":{"text":"inner default"}},
		  "9":{"class_type":"SaveImage","inputs":{"filename_prefix":"x","images":["57:8",0]}}
		}`))
	}))
	t.Cleanup(srv.Close)
	cl := comfy.New(srv.URL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	g, err := ConvertWith(context.Background(), cl, ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	clip := g["57:30"].(map[string]any)["inputs"].(map[string]any)["clip_name"]
	if clip != "qwen/qwen_3_4b.safetensors" {
		t.Fatalf("clip overlay %v", clip)
	}
	unet := g["57:28"].(map[string]any)["inputs"].(map[string]any)["unet_name"]
	if unet != "ZIT/z_image_turbo_bf16.safetensors" {
		t.Fatalf("unet overlay %v", unet)
	}
}

func testdataDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata")
}

func TestConvertWithPrefersComfyEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workflow/convert" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"6":{"class_type":"CLIPTextEncode","inputs":{"text":"from-comfy"}},"9":{"class_type":"SaveImage","inputs":{"filename_prefix":"x","images":["8",0]}}}`))
	}))
	t.Cleanup(srv.Close)
	cl := comfy.New(srv.URL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	ui := json.RawMessage(`{"nodes":[{"id":1,"type":"Note"}],"links":[]}`)
	g, err := ConvertWith(context.Background(), cl, ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	enc := g["6"].(map[string]any)["inputs"].(map[string]any)
	if enc["text"] != "from-comfy" {
		t.Fatalf("%v", g)
	}
}

func TestConvertWithFallsBackWhenEndpointMissing(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	cl := comfy.New(srv.URL, "", "cid", nil, 5*time.Second, 20*time.Millisecond, false)
	ui := json.RawMessage(`{
	  "nodes": [{"id": 6, "type": "CLIPTextEncode", "mode": 0,
	    "inputs": [{"name": "text", "widget": {"name": "text"}, "link": null}],
	    "widgets_values": ["local-fallback"]}],
	  "links": []
	}`)
	g, err := ConvertWith(context.Background(), cl, ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	enc := g["6"].(map[string]any)["inputs"].(map[string]any)
	if enc["text"] != "local-fallback" {
		t.Fatalf("%v", g)
	}
}

func TestConvertUIToAPI(t *testing.T) {
	ui := []byte(`{
	  "last_node_id": 9,
	  "nodes": [
	    {"id": 6, "type": "CLIPTextEncode", "mode": 0, "title": "Positive",
	      "inputs": [
	        {"name": "clip", "type": "CLIP", "link": 1},
	        {"name": "text", "type": "STRING", "widget": {"name": "text"}, "link": null}
	      ],
	      "widgets_values": ["a red cube"]
	    },
	    {"id": 9, "type": "SaveImage", "mode": 0,
	      "inputs": [
	        {"name": "images", "type": "IMAGE", "link": 2},
	        {"name": "filename_prefix", "type": "STRING", "widget": {"name": "filename_prefix"}, "link": null}
	      ],
	      "widgets_values": ["ComfyUI"]
	    },
	    {"id": 4, "type": "CheckpointLoaderSimple", "mode": 0,
	      "inputs": [
	        {"name": "ckpt_name", "type": "COMBO", "widget": {"name": "ckpt_name"}, "link": null}
	      ],
	      "outputs": [
	        {"name": "MODEL", "links": [3]},
	        {"name": "CLIP", "links": [1]},
	        {"name": "VAE", "links": [4]}
	      ],
	      "widgets_values": ["model.safetensors"]
	    }
	  ],
	  "links": [
	    [1, 4, 1, 6, 0, "CLIP"],
	    [2, 8, 0, 9, 0, "IMAGE"]
	  ]
	}`)
	info := map[string]comfy.NodeDef{
		"CLIPTextEncode": {
			Input: map[string]map[string]any{
				"required": {
					"text": []any{"STRING", map[string]any{"multiline": true}},
					"clip": []any{"CLIP", map[string]any{}},
				},
			},
			InputOrder: map[string][]string{"required": {"text", "clip"}},
		},
		"SaveImage": {
			Input: map[string]map[string]any{
				"required": {
					"images":          []any{"IMAGE", map[string]any{}},
					"filename_prefix": []any{"STRING", map[string]any{}},
				},
			},
			InputOrder: map[string][]string{"required": {"images", "filename_prefix"}},
			OutputNode: true,
		},
		"CheckpointLoaderSimple": {
			Input: map[string]map[string]any{
				"required": {"ckpt_name": []any{"COMBO", map[string]any{}}},
			},
			InputOrder: map[string][]string{"required": {"ckpt_name"}},
		},
	}
	g, err := Convert(ui, info)
	if err != nil {
		t.Fatal(err)
	}
	enc := g["6"].(map[string]any)["inputs"].(map[string]any)
	if enc["text"] != "a red cube" {
		t.Fatalf("prompt=%v", enc["text"])
	}
	clip, ok := enc["clip"].([]any)
	if !ok || clip[0] != "4" {
		t.Fatalf("clip link=%v", enc["clip"])
	}
	ckpt := g["4"].(map[string]any)["inputs"].(map[string]any)["ckpt_name"]
	if ckpt != "model.safetensors" {
		t.Fatalf("ckpt=%v", ckpt)
	}
	m, err := Infer("demo", "Demo.json", "Demo", g, info)
	if err != nil {
		t.Fatal(err)
	}
	if m.Modality != "image" || m.OutputNode != "9" {
		t.Fatalf("%+v", m)
	}
	if p := m.Param("prompt"); p == nil || p.MapsTo[0].Node != "6" {
		t.Fatalf("prompt %+v", p)
	}
}

func TestConvertExpandsSubgraph(t *testing.T) {
	const sg = "f2fdebf6-dfaf-43b6-9eb2-7f70613cfdc1"
	ui := []byte(`{
	  "nodes": [
	    {"id": 9, "type": "SaveImage", "mode": 0,
	      "inputs": [
	        {"name": "images", "type": "IMAGE", "link": 62},
	        {"name": "filename_prefix", "type": "STRING", "widget": {"name": "filename_prefix"}, "link": null}
	      ],
	      "widgets_values": ["ComfyUI"]
	    },
	    {"id": 57, "type": "` + sg + `", "mode": 0,
	      "inputs": [
	        {"name": "text", "type": "STRING", "widget": {"name": "text"}, "link": null},
	        {"name": "width", "type": "INT", "widget": {"name": "width"}, "link": null}
	      ],
	      "outputs": [{"name": "IMAGE", "type": "IMAGE", "links": [62]}],
	      "widgets_values": ["hello from outer", 768],
	      "widgets_values_named": {"text": "hello from outer", "width": 768}
	    }
	  ],
	  "links": [[62, 57, 0, 9, 0, "IMAGE"]],
	  "definitions": {
	    "subgraphs": [{
	      "id": "` + sg + `",
	      "name": "Text to Image",
	      "inputs": [
	        {"name": "text", "linkIds": [34]},
	        {"name": "width", "linkIds": [35]}
	      ],
	      "outputs": [{"name": "IMAGE", "linkIds": [16]}],
	      "nodes": [
	        {"id": 27, "type": "CLIPTextEncode", "mode": 0,
	          "inputs": [
	            {"name": "clip", "type": "CLIP", "link": null},
	            {"name": "text", "type": "STRING", "widget": {"name": "text"}, "link": 34}
	          ],
	          "widgets_values": ["inner default"]
	        },
	        {"id": 13, "type": "EmptySD3LatentImage", "mode": 0,
	          "inputs": [
	            {"name": "width", "type": "INT", "widget": {"name": "width"}, "link": 35},
	            {"name": "height", "type": "INT", "widget": {"name": "height"}, "link": null},
	            {"name": "batch_size", "type": "INT", "widget": {"name": "batch_size"}, "link": null}
	          ],
	          "widgets_values": [64, 64, 1]
	        },
	        {"id": 8, "type": "VAEDecode", "mode": 0,
	          "inputs": [
	            {"name": "samples", "type": "LATENT", "link": 99},
	            {"name": "vae", "type": "VAE", "link": null}
	          ],
	          "outputs": [{"name": "IMAGE", "type": "IMAGE", "links": [16]}]
	        }
	      ],
	      "links": [
	        {"id": 16, "origin_id": 8, "origin_slot": 0, "target_id": -20, "target_slot": 0, "type": "IMAGE"},
	        {"id": 34, "origin_id": -10, "origin_slot": 0, "target_id": 27, "target_slot": 1, "type": "STRING"},
	        {"id": 35, "origin_id": -10, "origin_slot": 1, "target_id": 13, "target_slot": 0, "type": "INT"},
	        {"id": 99, "origin_id": 13, "origin_slot": 0, "target_id": 8, "target_slot": 0, "type": "LATENT"}
	      ]
	    }]
	  }
	}`)
	g, err := Convert(ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g["57"]; ok {
		t.Fatalf("subgraph instance should be expanded: %v", keysOf(g))
	}
	enc := g["57:27"].(map[string]any)
	if enc["class_type"] != "CLIPTextEncode" {
		t.Fatalf("class %v", enc["class_type"])
	}
	text := enc["inputs"].(map[string]any)["text"]
	if text != "hello from outer" {
		t.Fatalf("promoted prompt=%v", text)
	}
	w := g["57:13"].(map[string]any)["inputs"].(map[string]any)["width"]
	if fmtNum(w) != 768 {
		t.Fatalf("width=%v", w)
	}
	img := g["9"].(map[string]any)["inputs"].(map[string]any)["images"]
	link, ok := img.([]any)
	if !ok || nodeIDString(link[0]) != "57:8" {
		t.Fatalf("SaveImage images=%v", img)
	}
	lat := g["57:8"].(map[string]any)["inputs"].(map[string]any)["samples"]
	sl, ok := lat.([]any)
	if !ok || nodeIDString(sl[0]) != "57:13" {
		t.Fatalf("VAEDecode samples=%v", lat)
	}
}

func TestConvertRejectsUnexpandedAPI(t *testing.T) {
	raw := json.RawMessage(`{"57":{"class_type":"f2fdebf6-dfaf-43b6-9eb2-7f70613cfdc1","inputs":{"text":"x"}},"9":{"class_type":"SaveImage","inputs":{"filename_prefix":"x"}}}`)
	_, err := Convert(raw, nil)
	if err == nil || !strings.Contains(err.Error(), "unexpanded subgraph") {
		t.Fatalf("err=%v", err)
	}
}

func TestSkipControlAfterGenerate(t *testing.T) {
	ui := []byte(`{
	  "nodes": [{
	    "id": 3, "type": "KSampler", "mode": 0,
	    "inputs": [
	      {"name": "model", "link": null},
	      {"name": "positive", "link": null},
	      {"name": "negative", "link": null},
	      {"name": "latent_image", "link": null},
	      {"name": "seed", "widget": {"name": "seed"}, "link": null},
	      {"name": "steps", "widget": {"name": "steps"}, "link": null},
	      {"name": "cfg", "widget": {"name": "cfg"}, "link": null},
	      {"name": "sampler_name", "widget": {"name": "sampler_name"}, "link": null},
	      {"name": "scheduler", "widget": {"name": "scheduler"}, "link": null},
	      {"name": "denoise", "widget": {"name": "denoise"}, "link": null}
	    ],
	    "widgets_values": [42, "randomize", 8, 1, "res_multistep", "simple", 1]
	  }],
	  "links": []
	}`)
	g, err := Convert(ui, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := g["3"].(map[string]any)["inputs"].(map[string]any)
	if fmtNum(in["seed"]) != 42 || fmtNum(in["steps"]) != 8 {
		t.Fatalf("%v", in)
	}
	if in["sampler_name"] != "res_multistep" || in["scheduler"] != "simple" {
		t.Fatalf("%v", in)
	}
}

func keysOf(m map[string]any) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	return k
}

func fmtNum(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	default:
		return 0
	}
}

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

func TestAPIPassthrough(t *testing.T) {
	raw := json.RawMessage(`{"9":{"class_type":"SaveImage","inputs":{"filename_prefix":"x","images":["8",0]}}}`)
	g, err := Convert(raw, nil)
	if err != nil || g["9"] == nil {
		t.Fatalf("%v %+v", err, g)
	}
}
