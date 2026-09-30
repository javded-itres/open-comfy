package convert

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
)

// RenderUIFromRaw turns an API prompt into a ComfyUI canvas document.
// ComfyUI lists every workflows/*.json, but a file without nodes/links opens
// as an empty graph. UI documents are returned unchanged.
func RenderUIFromRaw(raw json.RawMessage, info map[string]comfy.NodeDef) ([]byte, error) {
	if IsUIWorkflow(raw) {
		return raw, nil
	}
	g, err := catalog.UnwrapRaw(raw)
	if err != nil {
		return nil, err
	}
	return RenderUI(g, info)
}

// apiNode is one API-prompt node placed on the canvas.
type apiNode struct {
	key   string
	id    int
	class string
	title string
	in    map[string]any
}

// RenderUI lays API nodes out on a grid and rebuilds links from [node, slot] inputs.
func RenderUI(graph map[string]any, info map[string]comfy.NodeDef) ([]byte, error) {
	keys := make([]string, 0, len(graph))
	for k, v := range graph {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m["class_type"].(string); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, fmt.Errorf("workflow has no nodes")
	}
	used := map[int]bool{}
	nodes := make([]apiNode, 0, len(keys))
	byKey := map[string]int{}
	next := 1
	for _, k := range keys {
		m := graph[k].(map[string]any)
		id := 0
		if n, err := strconv.Atoi(k); err == nil && n > 0 && !used[n] {
			id = n
		} else {
			for used[next] {
				next++
			}
			id = next
			next++
		}
		used[id] = true
		in, _ := m["inputs"].(map[string]any)
		title := ""
		if meta, ok := m["_meta"].(map[string]any); ok {
			title, _ = meta["title"].(string)
		}
		class, _ := m["class_type"].(string)
		nodes = append(nodes, apiNode{key: k, id: id, class: class, title: title, in: in})
		byKey[k] = id
	}

	type link struct {
		id     int
		from   int
		fslot  int
		to     int
		tslot  int
		typ    string
		toKey  string
		toName string
	}
	var links []link
	linkOf := map[string]int{} // "toKey\x00name" -> link id
	nextLink := 1
	for _, n := range nodes {
		names := inputNames(info[n.class], n.in)
		for _, name := range names {
			val, ok := n.in[name]
			if !ok {
				continue
			}
			fromKey, slot, ok := apiInputLink(val, byKey)
			if !ok {
				continue
			}
			id := nextLink
			nextLink++
			links = append(links, link{
				id: id, from: byKey[fromKey], fslot: slot, to: n.id, tslot: 0,
				typ: socketType(specOf(info[n.class], name)), toKey: n.key, toName: name,
			})
			linkOf[n.key+"\x00"+name] = id
		}
	}
	outLinks := map[int][][]any{} // node id -> links per output slot
	maxLink := 0
	for _, l := range links {
		for len(outLinks[l.from]) <= l.fslot {
			outLinks[l.from] = append(outLinks[l.from], nil)
		}
		outLinks[l.from][l.fslot] = append(outLinks[l.from][l.fslot], l.id)
		if l.id > maxLink {
			maxLink = l.id
		}
	}

	maxID := 0
	uiNodes := make([]any, 0, len(nodes))
	inputIndex := map[int]map[string]int{}
	for i, n := range nodes {
		def := info[n.class]
		inputs, widgets, named, index := canvasInputs(n, def, byKey, linkOf)
		inputIndex[n.id] = index
		var outputs []any
		for _, ids := range outLinks[n.id] {
			if ids == nil {
				outputs = append(outputs, map[string]any{"name": "", "type": "*", "links": []any{}, "slot_index": len(outputs)})
				continue
			}
			outputs = append(outputs, map[string]any{
				"name": "", "type": "*", "links": ids, "slot_index": len(outputs),
			})
		}
		if outputs == nil {
			outputs = []any{}
		}
		if n.id > maxID {
			maxID = n.id
		}
		node := map[string]any{
			"id":                   n.id,
			"type":                 n.class,
			"pos":                  []float64{float64((i % 3) * 440), float64((i / 3) * 280)},
			"size":                 []float64{340, 160},
			"flags":                map[string]any{},
			"order":                i,
			"mode":                 0,
			"inputs":               inputs,
			"outputs":              outputs,
			"properties":           map[string]any{"Node name for S&R": n.class},
			"widgets_values":       widgets,
			"widgets_values_named": named,
		}
		if n.title != "" && n.title != n.class {
			node["title"] = n.title
		}
		uiNodes = append(uiNodes, node)
	}
	for i := range links {
		if idx, ok := inputIndex[links[i].to][links[i].toName]; ok {
			links[i].tslot = idx
		}
	}
	linkRows := make([]any, 0, len(links))
	for _, l := range links {
		linkRows = append(linkRows, []any{l.id, l.from, l.fslot, l.to, l.tslot, l.typ})
	}
	doc := map[string]any{
		"id":           fmt.Sprintf("opencomfy-%d", maxID),
		"revision":     0,
		"last_node_id": maxID,
		"last_link_id": maxLink,
		"nodes":        uiNodes,
		"links":        linkRows,
		"groups":       []any{},
		"config":       map[string]any{},
		"extra":        map[string]any{},
		"version":      0.4,
	}
	return json.Marshal(doc)
}

func canvasInputs(n apiNode, def comfy.NodeDef, byKey map[string]int, linkOf map[string]int) ([]any, []any, map[string]any, map[string]int) {
	var inputs []any
	var widgets []any
	named := map[string]any{}
	index := map[string]int{}
	add := func(name, typ string, link any, widget bool, value any) {
		entry := map[string]any{"name": name, "type": typ, "link": link}
		if widget {
			entry["widget"] = map[string]any{"name": name}
			widgets = append(widgets, value)
			named[name] = value
		}
		index[name] = len(inputs)
		inputs = append(inputs, entry)
	}
	// Widget order matches the node constructor: definition order, with dynamic
	// combo children inserted immediately after the combo. ComfyUI assigns
	// widgets_values to that list, so a missing slot shifts max_length.
	for _, name := range inputNames(def, n.in) {
		if _, done := index[name]; done {
			continue
		}
		spec := specOf(def, name)
		val, has := n.in[name]
		_, _, linked := apiInputLink(val, byKey)
		var link any
		if id, ok := linkOf[n.key+"\x00"+name]; ok {
			link = id
			linked = true
		}
		if isWidgetSpec(spec) {
			value := widgetValue(val, has && !linked, spec)
			key := comboKey(val, has, linked, spec)
			if dynamicCombo(spec) {
				if _, ok := value.(string); !ok && key != "" {
					value = key
				}
			}
			add(name, widgetType(spec), link, true, value)
			for _, child := range dynamicChildNames(spec, key) {
				cname := name + "." + child
				cspec := dynamicChildSpec(spec, key, child)
				cval, cok := n.in[cname]
				_, _, clinked := apiInputLink(cval, byKey)
				var clink any
				if id, ok := linkOf[n.key+"\x00"+cname]; ok {
					clink = id
					clinked = true
				}
				if isWidgetSpec(cspec) {
					add(cname, widgetType(cspec), clink, true, widgetValue(cval, cok && !clinked, cspec))
					continue
				}
				if clink != nil {
					add(cname, socketType(cspec), clink, false, nil)
				}
			}
			continue
		}
		if link != nil {
			add(name, socketType(spec), link, false, nil)
		}
	}
	if inputs == nil {
		inputs = []any{}
	}
	if widgets == nil {
		widgets = []any{}
	}
	return inputs, widgets, named, index
}

func dynamicCombo(spec any) bool {
	return strings.Contains(strings.ToUpper(widgetType(spec)), "DYNAMICCOMBO")
}

func comboKey(val any, has, linked bool, spec any) string {
	if has && !linked {
		if s, ok := val.(string); ok && s != "" {
			return s
		}
	}
	if d, ok := widgetDefault(spec); ok {
		if s, ok := d.(string); ok && s != "" {
			return s
		}
	}
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 {
		return ""
	}
	meta, _ := arr[1].(map[string]any)
	options, _ := meta["options"].([]any)
	if len(options) == 0 {
		return ""
	}
	first, _ := options[0].(map[string]any)
	s, _ := first["key"].(string)
	return s
}

func widgetType(spec any) string {
	arr, ok := spec.([]any)
	if !ok || len(arr) == 0 {
		return "*"
	}
	switch t := arr[0].(type) {
	case []any:
		return "COMBO"
	case string:
		if t == "" {
			return "*"
		}
		return t
	default:
		return "*"
	}
}

func widgetDefault(spec any) (any, bool) {
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 {
		return nil, false
	}
	meta, ok := arr[1].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := meta["default"]
	return v, ok
}

func widgetValue(val any, use bool, spec any) any {
	if use {
		return val
	}
	if d, ok := widgetDefault(spec); ok {
		return d
	}
	arr, ok := spec.([]any)
	if ok && len(arr) > 0 {
		if opts, ok := arr[0].([]any); ok && len(opts) > 0 {
			return opts[0]
		}
		if s, ok := arr[0].(string); ok {
			switch strings.ToUpper(s) {
			case "STRING":
				return ""
			case "BOOLEAN":
				return false
			case "INT", "FLOAT", "NUMBER":
				return 0
			}
		}
	}
	return nil
}

func dynamicChildSpec(spec any, key, child string) any {
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 {
		return []any{"*"}
	}
	meta, ok := arr[1].(map[string]any)
	if !ok {
		return []any{"*"}
	}
	options, _ := meta["options"].([]any)
	for _, opt := range options {
		om, ok := opt.(map[string]any)
		if !ok || om["key"] != key {
			continue
		}
		ins, _ := om["inputs"].(map[string]any)
		for _, group := range []string{"required", "optional"} {
			fields, _ := ins[group].(map[string]any)
			if s, ok := fields[child]; ok {
				return s
			}
		}
	}
	return []any{"*"}
}

func inputNames(def comfy.NodeDef, have map[string]any) []string {
	var names []string
	seen := map[string]bool{}
	for _, group := range []string{"required", "optional"} {
		order := def.InputOrder[group]
		if len(order) == 0 && def.Input[group] != nil {
			for k := range def.Input[group] {
				order = append(order, k)
			}
			sort.Strings(order)
		}
		for _, name := range order {
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	var extra []string
	for name := range have {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(names, extra...)
}

func specOf(def comfy.NodeDef, name string) any {
	for _, group := range []string{"required", "optional"} {
		if def.Input[group] == nil {
			continue
		}
		if spec, ok := def.Input[group][name]; ok {
			return spec
		}
	}
	return nil
}

func socketType(spec any) string {
	arr, ok := spec.([]any)
	if !ok || len(arr) == 0 {
		return "*"
	}
	s, ok := arr[0].(string)
	if !ok || s == "" {
		return "*"
	}
	return s
}

func apiInputLink(v any, ids map[string]int) (string, int, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) < 2 {
		return "", 0, false
	}
	id, ok := arr[0].(string)
	if !ok {
		return "", 0, false
	}
	if _, known := ids[id]; !known {
		return "", 0, false
	}
	return id, int(asFloat(arr[1])), true
}
