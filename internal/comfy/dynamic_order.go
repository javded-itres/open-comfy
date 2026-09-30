package comfy

import (
	"bytes"
	"encoding/json"
)

// annotateDynamicOrder records the declaration order of inputs nested under a
// dynamic combo. encoding/json drops object key order, and the canvas needs
// that order so max_length and sampling_mode.* line up with widgets_values.
func annotateDynamicOrder(info map[string]NodeDef, raw []byte) {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	for class, body := range root {
		def, ok := info[class]
		if !ok {
			continue
		}
		orders := dynamicOrders(body)
		if len(orders) == 0 {
			continue
		}
		for _, fields := range def.Input {
			for field, spec := range fields {
				ord, ok := orders[field]
				if !ok {
					continue
				}
				arr, ok := spec.([]any)
				if !ok || len(arr) < 2 {
					continue
				}
				meta, ok := arr[1].(map[string]any)
				if !ok {
					continue
				}
				boxed := map[string]any{}
				for key, names := range ord {
					anys := make([]any, len(names))
					for i, n := range names {
						anys[i] = n
					}
					boxed[key] = anys
				}
				meta["_oc_order"] = boxed
				arr[1] = meta
				fields[field] = arr
			}
		}
		info[class] = def
	}
}

func dynamicOrders(node json.RawMessage) map[string]map[string][]string {
	root, ok := parseObj(node)
	if !ok {
		return nil
	}
	input, ok := parseObj(root.m["input"])
	if !ok {
		return nil
	}
	out := map[string]map[string][]string{}
	for _, group := range []string{"required", "optional"} {
		fields, ok := parseObj(input.m[group])
		if !ok {
			continue
		}
		for _, field := range fields.keys {
			spec := fields.m[field]
			var arr []json.RawMessage
			if json.Unmarshal(spec, &arr) != nil || len(arr) < 2 {
				continue
			}
			meta, ok := parseObj(arr[1])
			if !ok {
				continue
			}
			var options []json.RawMessage
			if json.Unmarshal(meta.m["options"], &options) != nil {
				continue
			}
			for _, opt := range options {
				o, ok := parseObj(opt)
				if !ok {
					continue
				}
				var key string
				if json.Unmarshal(o.m["key"], &key) != nil || key == "" {
					continue
				}
				ins, ok := parseObj(o.m["inputs"])
				if !ok {
					continue
				}
				var names []string
				for _, g := range []string{"required", "optional"} {
					sub, ok := parseObj(ins.m[g])
					if !ok {
						continue
					}
					names = append(names, sub.keys...)
				}
				if len(names) == 0 {
					continue
				}
				if out[field] == nil {
					out[field] = map[string][]string{}
				}
				out[field][key] = names
			}
		}
	}
	return out
}

type orderedObj struct {
	keys []string
	m    map[string]json.RawMessage
}

func parseObj(b json.RawMessage) (orderedObj, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return orderedObj{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return orderedObj{}, false
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return orderedObj{}, false
	}
	o := orderedObj{m: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return orderedObj{}, false
		}
		key, ok := tok.(string)
		if !ok {
			return orderedObj{}, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return orderedObj{}, false
		}
		o.keys = append(o.keys, key)
		o.m[key] = raw
	}
	return o, true
}
