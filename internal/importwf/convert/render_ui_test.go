package convert

import (
	"encoding/json"
	"testing"

	"github.com/javded-itres/open-comfy/internal/comfy"
)

func TestRenderUIFromAPIPrompt(t *testing.T) {
	raw := json.RawMessage(`{
	  "30:10": {"class_type":"UNETLoader","inputs":{"unet_name":"krea.safetensors","weight_dtype":"default"}},
	  "30:11": {"class_type":"CLIPLoader","inputs":{"clip_name":"qwen.safetensors","type":"stable_diffusion"}},
	  "9": {"class_type":"CLIPTextEncode","inputs":{"text":"a cube","clip":["30:11",0]}},
	  "8": {"class_type":"KSampler","inputs":{"model":["30:10",0],"positive":["9",0],"seed":1}}
	}`)
	info := map[string]comfy.NodeDef{
		"UNETLoader": {
			Input: map[string]map[string]any{
				"required": {
					"unet_name":    []any{[]any{"krea.safetensors"}},
					"weight_dtype": []any{[]any{"default"}},
				},
			},
			InputOrder: map[string][]string{"required": {"unet_name", "weight_dtype"}},
		},
		"CLIPLoader": {
			Input: map[string]map[string]any{
				"required": {
					"clip_name": []any{[]any{"qwen.safetensors"}},
					"type":      []any{[]any{"stable_diffusion"}},
				},
			},
			InputOrder: map[string][]string{"required": {"clip_name", "type"}},
		},
		"CLIPTextEncode": {
			Input: map[string]map[string]any{
				"required": {
					"text": []any{"STRING"},
					"clip": []any{"CLIP"},
				},
			},
			InputOrder: map[string][]string{"required": {"text", "clip"}},
		},
		"KSampler": {
			Input: map[string]map[string]any{
				"required": {
					"model":    []any{"MODEL"},
					"positive": []any{"CONDITIONING"},
					"seed":     []any{"INT"},
				},
			},
			InputOrder: map[string][]string{"required": {"model", "positive", "seed"}},
		},
	}
	out, err := RenderUIFromRaw(raw, info)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nodes []struct {
			ID     int    `json:"id"`
			Type   string `json:"type"`
			Inputs []struct {
				Name string `json:"name"`
				Link int    `json:"link"`
			} `json:"inputs"`
			Widgets []any `json:"widgets_values"`
		} `json:"nodes"`
		Links [][]any `json:"links"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes) != 4 {
		t.Fatalf("nodes %d", len(doc.Nodes))
	}
	if len(doc.Links) != 3 {
		t.Fatalf("links %d", len(doc.Links))
	}
	var encWidgets []any
	var encID int
	for _, n := range doc.Nodes {
		if n.Type == "CLIPTextEncode" {
			encWidgets = n.Widgets
			encID = n.ID
			if len(n.Inputs) != 2 || n.Inputs[0].Name != "text" || n.Inputs[0].Link != 0 {
				t.Fatalf("text widget %+v", n.Inputs)
			}
			if n.Inputs[1].Name != "clip" || n.Inputs[1].Link == 0 {
				t.Fatalf("clip link %+v", n.Inputs)
			}
		}
		if n.Type == "KSampler" {
			if len(n.Widgets) != 1 || n.Widgets[0] != float64(1) {
				t.Fatalf("sampler widgets %#v", n.Widgets)
			}
			var sawSeed bool
			for _, in := range n.Inputs {
				if in.Name == "seed" {
					sawSeed = true
				}
			}
			if !sawSeed {
				t.Fatalf("sampler inputs %+v", n.Inputs)
			}
		}
	}
	if len(encWidgets) != 1 || encWidgets[0] != "a cube" {
		t.Fatalf("widgets %#v", encWidgets)
	}
	for _, l := range doc.Links {
		if len(l) < 5 {
			continue
		}
		to, _ := l[3].(float64)
		slot, _ := l[4].(float64)
		if int(to) == encID && slot != 1 {
			t.Fatalf("clip target slot %v link %v", slot, l)
		}
	}
	if !IsUIWorkflow(out) {
		t.Fatal("rendered document is not a UI workflow")
	}
}

func TestRenderUITextGenerateKeepsMaxLength(t *testing.T) {
	raw := json.RawMessage(`{
	  "11": {"class_type":"CLIPLoader","inputs":{"clip_name":"qwen.safetensors"}},
	  "16": {"class_type":"TextGenerate","inputs":{
	    "clip":["11",0],
	    "prompt":["17",0],
	    "max_length":512,
	    "sampling_mode":"on",
	    "sampling_mode.temperature":0.7,
	    "sampling_mode.top_k":64,
	    "sampling_mode.top_p":0.95,
	    "sampling_mode.min_p":0.05,
	    "sampling_mode.repetition_penalty":1.05,
	    "sampling_mode.seed":0,
	    "sampling_mode.presence_penalty":0.0,
	    "thinking":false,
	    "use_default_template":true
	  }},
	  "17": {"class_type":"PrimitiveStringMultiline","inputs":{"value":"a white yeti"}}
	}`)
	on := map[string]any{
		"temperature":        []any{"FLOAT", map[string]any{"default": 0.7}},
		"top_k":              []any{"INT", map[string]any{"default": 64}},
		"top_p":              []any{"FLOAT", map[string]any{"default": 0.95}},
		"min_p":              []any{"FLOAT", map[string]any{"default": 0.05}},
		"repetition_penalty": []any{"FLOAT", map[string]any{"default": 1.05}},
		"seed":               []any{"INT", map[string]any{"default": 0}},
		"presence_penalty":   []any{"FLOAT", map[string]any{"default": 0.0}},
	}
	order := []any{"temperature", "top_k", "top_p", "min_p", "repetition_penalty", "seed", "presence_penalty"}
	info := map[string]comfy.NodeDef{
		"CLIPLoader": {
			Input:      map[string]map[string]any{"required": {"clip_name": []any{[]any{"qwen.safetensors"}}}},
			InputOrder: map[string][]string{"required": {"clip_name"}},
		},
		"PrimitiveStringMultiline": {
			Input:      map[string]map[string]any{"required": {"value": []any{"STRING", map[string]any{"default": ""}}}},
			InputOrder: map[string][]string{"required": {"value"}},
		},
		"TextGenerate": {
			Input: map[string]map[string]any{
				"required": {
					"clip":       []any{"CLIP"},
					"prompt":     []any{"STRING", map[string]any{"default": ""}},
					"max_length": []any{"INT", map[string]any{"default": 512}},
					"sampling_mode": []any{"COMFY_DYNAMICCOMBO_V3", map[string]any{
						"options": []any{
							map[string]any{"key": "on", "inputs": map[string]any{"required": on}},
							map[string]any{"key": "off", "inputs": map[string]any{}},
						},
						"_oc_order": map[string]any{"on": order},
					}},
				},
				"optional": {
					"thinking":             []any{"BOOLEAN", map[string]any{"default": false}},
					"use_default_template": []any{"BOOLEAN", map[string]any{"default": true}},
				},
			},
			InputOrder: map[string][]string{
				"required": {"clip", "prompt", "max_length", "sampling_mode"},
				"optional": {"thinking", "use_default_template"},
			},
		},
	}
	out, err := RenderUIFromRaw(raw, info)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Nodes []struct {
			Type   string `json:"type"`
			Inputs []struct {
				Name   string         `json:"name"`
				Type   string         `json:"type"`
				Widget map[string]any `json:"widget"`
			} `json:"inputs"`
			Widgets []any          `json:"widgets_values"`
			Named   map[string]any `json:"widgets_values_named"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var tg *struct {
		Type   string `json:"type"`
		Inputs []struct {
			Name   string         `json:"name"`
			Type   string         `json:"type"`
			Widget map[string]any `json:"widget"`
		} `json:"inputs"`
		Widgets []any          `json:"widgets_values"`
		Named   map[string]any `json:"widgets_values_named"`
	}
	for i := range doc.Nodes {
		if doc.Nodes[i].Type == "TextGenerate" {
			tg = &doc.Nodes[i]
		}
	}
	if tg == nil {
		t.Fatal("missing TextGenerate")
	}
	want := []string{
		"clip", "prompt", "max_length", "sampling_mode",
		"sampling_mode.temperature", "sampling_mode.top_k", "sampling_mode.top_p",
		"sampling_mode.min_p", "sampling_mode.repetition_penalty", "sampling_mode.seed",
		"sampling_mode.presence_penalty", "thinking", "use_default_template",
	}
	if len(tg.Inputs) != len(want) {
		t.Fatalf("inputs %d %#v", len(tg.Inputs), namesOf(tg.Inputs))
	}
	for i, name := range want {
		if tg.Inputs[i].Name != name {
			t.Fatalf("input %d = %s want %s", i, tg.Inputs[i].Name, name)
		}
		if name != "clip" && tg.Inputs[i].Widget == nil {
			t.Fatalf("%s is not a widget", name)
		}
	}
	if tg.Inputs[2].Type != "INT" {
		t.Fatalf("max_length type %s", tg.Inputs[2].Type)
	}
	if tg.Named["max_length"] != float64(512) {
		t.Fatalf("max_length %#v", tg.Named["max_length"])
	}
	if tg.Named["sampling_mode"] != "on" {
		t.Fatalf("sampling_mode %#v", tg.Named["sampling_mode"])
	}
	if tg.Named["sampling_mode.temperature"] != 0.7 {
		t.Fatalf("temperature %#v", tg.Named["sampling_mode.temperature"])
	}
	if len(tg.Widgets) != 12 {
		t.Fatalf("widgets %d %#v", len(tg.Widgets), tg.Widgets)
	}
	if tg.Widgets[1] != float64(512) || tg.Widgets[2] != "on" {
		t.Fatalf("value order %#v", tg.Widgets)
	}
}

func namesOf(in []struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Widget map[string]any `json:"widget"`
}) []string {
	out := make([]string, len(in))
	for i, n := range in {
		out[i] = n.Name
	}
	return out
}
