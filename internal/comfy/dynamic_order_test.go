package comfy

import (
	"encoding/json"
	"testing"
)

func TestAnnotateDynamicOrderKeepsChildOrder(t *testing.T) {
	raw := []byte(`{
	  "TextGenerate": {
	    "input": {
	      "required": {
	        "sampling_mode": ["COMFY_DYNAMICCOMBO_V3", {
	          "options": [
	            {"key": "on", "inputs": {"required": {
	              "temperature": ["FLOAT", {"default": 0.7}],
	              "top_k": ["INT", {"default": 64}],
	              "seed": ["INT", {"default": 0}]
	            }, "optional": {"presence_penalty": ["FLOAT", {"default": 0.0}]}}},
	            {"key": "off", "inputs": {}}
	          ]
	        }]
	      }
	    },
	    "input_order": {"required": ["sampling_mode"]}
	  }
	}`)
	var info map[string]NodeDef
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	annotateDynamicOrder(info, raw)
	spec := info["TextGenerate"].Input["required"]["sampling_mode"].([]any)
	meta := spec[1].(map[string]any)
	ord := meta["_oc_order"].(map[string]any)
	got := ord["on"].([]any)
	want := []string{"temperature", "top_k", "seed", "presence_penalty"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
