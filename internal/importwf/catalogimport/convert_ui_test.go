package catalogimport

import (
	"testing"

	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/importwf/convert"
)

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
	g, err := convert.Convert(ui, info)
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
