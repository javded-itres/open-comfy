package importwf

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/javded-itres/open-comfy/internal/comfy"
)

func TestMain(m *testing.M) {
	UseEmptyNodeMap()
	os.Exit(m.Run())
}

func TestSafeWorkflowName(t *testing.T) {
	n, err := SafeWorkflowName("../foo/bar.json")
	if err != nil || n != "bar.json" {
		t.Fatalf("%q %v", n, err)
	}
	n, err = SafeWorkflowName("My Graph")
	if err != nil || n != "My Graph.json" {
		t.Fatalf("%q %v", n, err)
	}
	if _, err := SafeWorkflowName(""); err == nil {
		t.Fatal("empty")
	}
}

func TestAnalyzeMissingNodeAndModel(t *testing.T) {
	info := map[string]comfy.NodeDef{
		"CLIPLoader": {
			Input: map[string]map[string]any{
				"required": {
					"clip_name": []any{[]any{"qwen/qwen_3_4b.safetensors", "other.safetensors"}},
				},
			},
		},
		"SaveImage": {OutputNode: true},
	}
	ui := json.RawMessage(`{
	  "nodes": [
	    {"id": 1, "type": "CLIPLoader", "inputs": [{"name": "clip_name", "widget": {"name": "clip_name"}}],
	      "widgets_values_named": {"clip_name": "missing.safetensors"}},
	    {"id": 2, "type": "SaveImage"},
	    {"id": 3, "type": "TotallyFakeNode"}
	  ],
	  "links": []
	}`)
	a := Analyze(ui, info, nil, "")
	if a.Format != "ui" {
		t.Fatalf("format %s", a.Format)
	}
	if a.Ready {
		t.Fatal("should not be ready")
	}
	if len(a.MissingNodes) != 1 || a.MissingNodes[0].Class != "TotallyFakeNode" {
		t.Fatalf("nodes %+v", a.MissingNodes)
	}
	if len(a.MissingModels) != 1 || a.MissingModels[0].Value != "missing.safetensors" {
		t.Fatalf("models %+v", a.MissingModels)
	}
}

func TestAnalyzeReadyWhenComboHasPrefix(t *testing.T) {
	info := map[string]comfy.NodeDef{
		"UNETLoader": {
			Input: map[string]map[string]any{
				"required": {
					"unet_name": []any{[]any{"ZIT/z_image_turbo_bf16.safetensors"}},
				},
			},
		},
	}
	api := json.RawMessage(`{"1":{"class_type":"UNETLoader","inputs":{"unet_name":"ZIT/z_image_turbo_bf16.safetensors"}}}`)
	a := Analyze(api, info, nil, "")
	if !a.Ready || len(a.MissingModels) != 0 || len(a.MissingNodes) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestAnalyzeSubgraphLoadersWithoutInputWidgets(t *testing.T) {
	info := map[string]comfy.NodeDef{
		"UNETLoader": {
			Input: map[string]map[string]any{
				"required": {
					"unet_name":    []any{[]any{"other.safetensors"}},
					"weight_dtype": []any{[]any{"default", "fp8_e4m3fn"}},
				},
			},
			InputOrder: map[string][]string{"required": {"unet_name", "weight_dtype"}},
		},
		"DualCLIPLoader": {
			Input: map[string]map[string]any{
				"required": {
					"clip_name1": []any{[]any{"other_clip.safetensors"}},
					"clip_name2": []any{[]any{"other_t5.safetensors"}},
					"type":       []any{"COMBO", map[string]any{"options": []any{"flux"}}},
				},
			},
			InputOrder: map[string][]string{"required": {"clip_name1", "clip_name2", "type"}},
		},
		"VAELoader": {
			Input: map[string]map[string]any{
				"required": {
					"vae_name": []any{[]any{"other_vae.safetensors"}},
				},
			},
			InputOrder: map[string][]string{"required": {"vae_name"}},
		},
		"SaveImage": {OutputNode: true},
	}
	ui := json.RawMessage(`{
	  "nodes": [
	    {"id": 9, "type": "SaveImage", "widgets_values": ["flux_krea"]},
	    {"id": 53, "type": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "widgets_values": ["a cat", 1024, 1024]}
	  ],
	  "definitions": {
	    "subgraphs": {
	      "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": {
	        "nodes": [
	          {"id": 38, "type": "UNETLoader", "inputs": [], "widgets_values": ["flux1-krea-dev_fp8_scaled.safetensors", "default"]},
	          {"id": 40, "type": "DualCLIPLoader", "inputs": [], "widgets_values": ["clip_l.safetensors", "t5xxl_fp16.safetensors", "flux"]},
	          {"id": 39, "type": "VAELoader", "inputs": [], "widgets_values": ["ae.safetensors"]}
	        ]
	      }
	    }
	  }
	}`)
	a := Analyze(ui, info, nil, "")
	got := map[string]string{}
	for _, m := range a.MissingModels {
		got[m.Value] = m.Field
	}
	if got["flux1-krea-dev_fp8_scaled.safetensors"] != "unet_name" {
		t.Fatalf("unet %+v", a.MissingModels)
	}
	if got["clip_l.safetensors"] != "clip_name1" || got["t5xxl_fp16.safetensors"] != "clip_name2" {
		t.Fatalf("clip %+v", a.MissingModels)
	}
	if got["ae.safetensors"] != "vae_name" {
		t.Fatalf("vae %+v", a.MissingModels)
	}
}

func TestPackAllowed(t *testing.T) {
	git := "https://github.com/kijai/ComfyUI-KJNodes.git"
	if !PackAllowed(git, nil) {
		t.Fatal("default allow")
	}
	if PackAllowed("https://github.com/evil/malware", nil) {
		t.Fatal("evil")
	}
}
