package catalogimport

import (
	"testing"

	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/importwf/convert"
)

func TestInferReadsTextGenerateWidgets(t *testing.T) {
	ui := []byte(`{
	  "nodes": [
	    {"id": 17, "type": "PrimitiveStringMultiline", "title": "Prompt",
	      "inputs": [{"name": "value", "type": "STRING", "widget": {"name": "value"}, "link": null}],
	      "widgets_values": ["a white yeti"]},
	    {"id": 16, "type": "TextGenerate",
	      "inputs": [
	        {"name": "clip", "type": "CLIP", "link": 1},
	        {"name": "prompt", "type": "STRING", "widget": {"name": "prompt"}, "link": 2},
	        {"name": "max_length", "type": "INT", "widget": {"name": "max_length"}, "link": null},
	        {"name": "sampling_mode", "type": "COMFY_DYNAMICCOMBO_V3", "widget": {"name": "sampling_mode"}, "link": null},
	        {"name": "sampling_mode.temperature", "type": "FLOAT", "widget": {"name": "sampling_mode.temperature"}, "link": null},
	        {"name": "sampling_mode.top_k", "type": "INT", "widget": {"name": "sampling_mode.top_k"}, "link": null},
	        {"name": "thinking", "type": "BOOLEAN", "widget": {"name": "thinking"}, "link": null}
	      ],
	      "widgets_values": ["", 512, "on", 0.7, 64, false]},
	    {"id": 9, "type": "SaveImage",
	      "inputs": [
	        {"name": "images", "type": "IMAGE", "link": 3},
	        {"name": "filename_prefix", "type": "STRING", "widget": {"name": "filename_prefix"}, "link": null}
	      ],
	      "widgets_values": ["krea"]}
	  ],
	  "links": [[1, 11, 0, 16, 0, "CLIP"], [2, 17, 0, 16, 1, "STRING"], [3, 8, 0, 9, 0, "IMAGE"]]
	}`)
	on := map[string]any{
		"temperature": []any{"FLOAT", map[string]any{"default": 0.7, "min": 0.01, "max": 2.0}},
		"top_k":       []any{"INT", map[string]any{"default": 64, "min": 0, "max": 1000}},
	}
	info := map[string]comfy.NodeDef{
		"PrimitiveStringMultiline": {
			Input:      map[string]map[string]any{"required": {"value": []any{"STRING", map[string]any{"default": ""}}}},
			InputOrder: map[string][]string{"required": {"value"}},
		},
		"SaveImage": {
			Input: map[string]map[string]any{"required": {
				"images":          []any{"IMAGE"},
				"filename_prefix": []any{"STRING", map[string]any{"default": "ComfyUI"}},
			}},
			InputOrder: map[string][]string{"required": {"images", "filename_prefix"}},
			OutputNode: true,
		},
		"TextGenerate": {
			Input: map[string]map[string]any{
				"required": {
					"clip":       []any{"CLIP"},
					"prompt":     []any{"STRING", map[string]any{"default": ""}},
					"max_length": []any{"INT", map[string]any{"default": 512, "min": 1, "max": 32768}},
					"sampling_mode": []any{"COMFY_DYNAMICCOMBO_V3", map[string]any{
						"options": []any{
							map[string]any{"key": "on", "inputs": map[string]any{"required": on}},
							map[string]any{"key": "off", "inputs": map[string]any{}},
						},
						"_oc_order": map[string]any{"on": []any{"temperature", "top_k"}},
					}},
				},
				"optional": {"thinking": []any{"BOOLEAN", map[string]any{"default": false}}},
			},
			InputOrder: map[string][]string{
				"required": {"clip", "prompt", "max_length", "sampling_mode"},
				"optional": {"thinking"},
			},
		},
	}
	g, err := convert.Convert(ui, info)
	if err != nil {
		t.Fatal(err)
	}
	tg := g["16"].(map[string]any)["inputs"].(map[string]any)
	if tg["max_length"] != float64(512) || tg["sampling_mode"] != "on" || tg["sampling_mode.temperature"] != 0.7 {
		t.Fatalf("widgets %#v", tg)
	}
	m, err := Infer("krea", "krea.json", "krea", g, info)
	if err != nil {
		t.Fatal(err)
	}
	ml := m.Param("max_length")
	if ml == nil || ml.Type != "integer" || ml.Default != 512 || ml.MapsTo[0].Node != "16" || ml.MapsTo[0].Field != "max_length" {
		t.Fatalf("max_length %+v", ml)
	}
	if ml.Min == nil || *ml.Min != 1 || ml.Max == nil || *ml.Max != 32768 {
		t.Fatalf("range %+v %+v", ml.Min, ml.Max)
	}
	mode := m.Param("sampling_mode")
	if mode == nil || mode.Default != "on" || len(mode.Enum) != 2 {
		t.Fatalf("sampling_mode %+v", mode)
	}
	temp := m.Param("sampling_mode.temperature")
	if temp == nil || temp.Type != "number" || temp.Default != 0.7 || temp.MapsTo[0].Field != "sampling_mode.temperature" {
		t.Fatalf("temperature %+v", temp)
	}
	if m.Param("sampling_mode.top_k") == nil || m.Param("thinking") == nil {
		var got []string
		for _, p := range m.Parameters {
			got = append(got, p.Name)
		}
		t.Fatalf("params %v", got)
	}
	if m.Param("prompt") == nil || m.Param("prompt").MapsTo[0].Node != "17" {
		t.Fatalf("prompt %+v", m.Param("prompt"))
	}
	if m.Modality != "image" {
		t.Fatalf("modality %s", m.Modality)
	}
}

func TestPreviewAnyIsNotWanVideo(t *testing.T) {
	g := map[string]any{
		"1": map[string]any{"class_type": "PreviewAny", "inputs": map[string]any{"source": []any{"2", 0}}},
		"2": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "cat"}},
		"9": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"filename_prefix": "x"}},
	}
	m, err := Infer("krea", "krea.json", "krea", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Modality != "image" {
		t.Fatalf("modality %s", m.Modality)
	}
	wan := map[string]any{
		"1": map[string]any{"class_type": "WanImageToVideo", "inputs": map[string]any{"prompt": "cat"}},
		"9": map[string]any{"class_type": "SaveVideo", "inputs": map[string]any{"filename_prefix": "x"}},
	}
	mv, err := Infer("wan", "wan.json", "wan", wan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mv.Modality != "video" {
		t.Fatalf("wan modality %s", mv.Modality)
	}
}
