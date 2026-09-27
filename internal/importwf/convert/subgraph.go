package convert

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	subgraphInputNodeID  = -10
	subgraphOutputNodeID = -20
)

type linkRec struct {
	ID         int
	OriginID   any
	OriginSlot int
	TargetID   any
	TargetSlot int
	Type       string
}

type subgraphInst struct {
	outerID string
	outputs map[int]linkRec
	inputs  map[int][]linkRec
}

func expandUIWorkflow(raw json.RawMessage) (json.RawMessage, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	defs := SubgraphDefs(root)
	if len(defs) == 0 {
		return raw, nil
	}
	for round := 0; round < 8; round++ {
		nodes, _ := root["nodes"].([]any)
		links := parseLinks(root["links"])
		var next []any
		changed := false
		maxLink := 0
		usedIDs := map[int]bool{}
		for _, l := range links {
			usedIDs[l.ID] = true
			if l.ID > maxLink {
				maxLink = l.ID
			}
		}
		var exploded []subgraphInst
		for _, n := range nodes {
			nm, ok := n.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := nm["type"].(string)
			def, isSG := defs[typ]
			if !isSG || !CatalogUUID(typ) {
				next = append(next, n)
				continue
			}
			changed = true
			outerID := NodeIDString(nm["id"])
			innerNodes, innerLinks, inMap, outMap, err := explodeSubgraph(nm, def, outerID, &maxLink, usedIDs)
			if err != nil {
				return nil, err
			}
			next = append(next, innerNodes...)
			links = append(links, innerLinks...)
			exploded = append(exploded, subgraphInst{outerID: outerID, outputs: outMap, inputs: inMap})
		}
		if !changed {
			break
		}
		var rewritten []linkRec
		for _, l := range links {
			if isIONode(l.OriginID) || isIONode(l.TargetID) {
				continue
			}
			if st, ok := findInst(exploded, l.OriginID); ok {
				rep, ok := st.outputs[l.OriginSlot]
				if !ok {
					continue
				}
				l.OriginID = rep.OriginID
				l.OriginSlot = rep.OriginSlot
			}
			if st, ok := findInst(exploded, l.TargetID); ok {
				targets := st.inputs[l.TargetSlot]
				if len(targets) == 0 {
					continue
				}
				for i, t := range targets {
					cp := l
					if i > 0 {
						maxLink++
						cp.ID = maxLink
					}
					cp.TargetID = t.TargetID
					cp.TargetSlot = t.TargetSlot
					rewritten = append(rewritten, cp)
				}
				continue
			}
			rewritten = append(rewritten, l)
		}
		root["nodes"] = next
		root["links"] = dumpLinks(rewritten)
	}
	if n := leftoverUUIDNode(root["nodes"]); n != "" {
		return nil, fmt.Errorf("unexpanded subgraph node %s (missing definitions.subgraphs)", n)
	}
	return json.Marshal(root)
}

func findInst(list []subgraphInst, id any) (subgraphInst, bool) {
	want := NodeIDString(id)
	for _, it := range list {
		if it.outerID == want {
			return it, true
		}
	}
	return subgraphInst{}, false
}

func explodeSubgraph(instance, def map[string]any, outerID string, maxLink *int, usedIDs map[int]bool) (nodes []any, links []linkRec, inMap map[int][]linkRec, outMap map[int]linkRec, err error) {
	inMap = map[int][]linkRec{}
	outMap = map[int]linkRec{}
	values := InstanceValues(instance)
	sgInputs, _ := def["inputs"].([]any)
	sgOutputs, _ := def["outputs"].([]any)
	innerNodes, _ := def["nodes"].([]any)
	innerLinks := parseLinks(def["links"])
	linkByID := map[int]linkRec{}
	for _, l := range innerLinks {
		linkByID[l.ID] = l
	}

	idMap := map[string]string{}
	for _, n := range innerNodes {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		old := NodeIDString(nm["id"])
		if old == strconv.Itoa(subgraphInputNodeID) || old == strconv.Itoa(subgraphOutputNodeID) {
			continue
		}
		cp := cloneMap(nm)
		newID := outerID + ":" + old
		cp["id"] = newID
		idMap[old] = newID
		nodes = append(nodes, cp)
	}

	remapID := func(id any) any {
		if isIONode(id) {
			return id
		}
		s := NodeIDString(id)
		if n, ok := idMap[s]; ok {
			return n
		}
		return id
	}

	for _, l := range innerLinks {
		if isIONode(l.OriginID) || isIONode(l.TargetID) {
			continue
		}
		id := l.ID
		target := remapID(l.TargetID)
		if usedIDs[id] {
			*maxLink++
			id = *maxLink
			patchInputLink(nodes, NodeIDString(target), l.TargetSlot, id)
		}
		usedIDs[id] = true
		if id > *maxLink {
			*maxLink = id
		}
		links = append(links, linkRec{
			ID:         id,
			OriginID:   remapID(l.OriginID),
			OriginSlot: l.OriginSlot,
			TargetID:   target,
			TargetSlot: l.TargetSlot,
			Type:       l.Type,
		})
	}

	slotByName := map[string]int{}
	instInputs, _ := instance["inputs"].([]any)
	for i, in := range instInputs {
		im, _ := in.(map[string]any)
		name, _ := im["name"].(string)
		if name != "" {
			slotByName[name] = i
		}
	}

	for idx, in := range sgInputs {
		im, _ := in.(map[string]any)
		if im == nil {
			continue
		}
		name, _ := im["name"].(string)
		outerSlot := idx
		if s, ok := slotByName[name]; ok {
			outerSlot = s
		}
		for _, lid := range asIntList(im["linkIds"]) {
			l, ok := linkByID[lid]
			if !ok || isIONode(l.TargetID) {
				continue
			}
			target := remapID(l.TargetID)
			rec := linkRec{TargetID: target, TargetSlot: l.TargetSlot, Type: l.Type}
			inMap[outerSlot] = append(inMap[outerSlot], rec)
			if v, ok := values[name]; ok {
				applyPromotedValue(nodes, NodeIDString(target), l.TargetSlot, name, v)
			}
		}
	}
	for idx, out := range sgOutputs {
		om, _ := out.(map[string]any)
		if om == nil {
			continue
		}
		for _, lid := range asIntList(om["linkIds"]) {
			l, ok := linkByID[lid]
			if !ok || isIONode(l.OriginID) {
				continue
			}
			outMap[idx] = linkRec{OriginID: remapID(l.OriginID), OriginSlot: l.OriginSlot, Type: l.Type}
		}
	}
	return nodes, links, inMap, outMap, nil
}

func patchInputLink(nodes []any, nid string, slot, newID int) {
	for _, n := range nodes {
		nm, _ := n.(map[string]any)
		if NodeIDString(nm["id"]) != nid {
			continue
		}
		inputs, ok := nm["inputs"].([]any)
		if !ok || slot < 0 || slot >= len(inputs) {
			return
		}
		im, _ := inputs[slot].(map[string]any)
		if im == nil {
			return
		}
		im["link"] = newID
		inputs[slot] = im
		nm["inputs"] = inputs
		return
	}
}

// overlaySubgraphPromoted copies widget values from subgraph *instances*
// (folder-prefixed combo paths, seed, prompt, …) onto the expanded API graph.
// ComfyUI POST /workflow/convert often keeps the inner-node basename defaults
// (qwen_3_4b.safetensors) which fail validation against models/qwen/….
func overlaySubgraphPromoted(ui json.RawMessage, graph map[string]any) {
	if len(ui) == 0 || graph == nil {
		return
	}
	var root map[string]any
	if json.Unmarshal(ui, &root) != nil {
		return
	}
	defs := SubgraphDefs(root)
	if len(defs) == 0 {
		return
	}
	overlaySubgraphPromotedNodes(root["nodes"], "", defs, graph)
}

func overlaySubgraphPromotedNodes(nodes any, prefix string, defs map[string]map[string]any, graph map[string]any) {
	arr, _ := nodes.([]any)
	for _, n := range arr {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := nm["type"].(string)
		def, ok := defs[typ]
		if !ok || !CatalogUUID(typ) {
			continue
		}
		outerID := NodeIDString(nm["id"])
		if prefix != "" {
			outerID = prefix + ":" + outerID
		}
		values := InstanceValues(nm)
		if len(values) == 0 {
			continue
		}
		innerByID := map[string]map[string]any{}
		for _, in := range asAnySlice(def["nodes"]) {
			im, _ := in.(map[string]any)
			if im != nil {
				innerByID[NodeIDString(im["id"])] = im
			}
		}
		linkByID := map[int]linkRec{}
		for _, l := range parseLinks(def["links"]) {
			linkByID[l.ID] = l
		}
		for _, in := range asAnySlice(def["inputs"]) {
			im, _ := in.(map[string]any)
			name, _ := im["name"].(string)
			v, has := values[name]
			if !has || name == "" {
				continue
			}
			for _, lid := range asIntList(im["linkIds"]) {
				l, ok := linkByID[lid]
				if !ok || isIONode(l.TargetID) {
					continue
				}
				innerLocal := NodeIDString(l.TargetID)
				field := name
				if inode := innerByID[innerLocal]; inode != nil {
					if s := inputNameAtSlot(inode, l.TargetSlot); s != "" {
						field = s
					}
				}
				setAPIScalar(graph, outerID+":"+innerLocal, field, v)
				setAPIScalar(graph, innerLocal, field, v)
			}
		}
		overlaySubgraphPromotedNodes(def["nodes"], outerID, defs, graph)
	}
}

func asAnySlice(v any) []any {
	a, _ := v.([]any)
	return a
}

func inputNameAtSlot(node map[string]any, slot int) string {
	inputs, _ := node["inputs"].([]any)
	if slot < 0 || slot >= len(inputs) {
		return ""
	}
	im, _ := inputs[slot].(map[string]any)
	s, _ := im["name"].(string)
	return s
}

func setAPIScalar(graph map[string]any, nid, field string, value any) {
	node, ok := graph[nid].(map[string]any)
	if !ok {
		return
	}
	inputs, ok := node["inputs"].(map[string]any)
	if !ok || inputs == nil {
		return
	}
	if cur, exists := inputs[field]; exists {
		if _, isLink := cur.([]any); isLink {
			return
		}
	}
	inputs[field] = value
}

func applyPromotedValue(nodes []any, nid string, slot int, fallbackName string, value any) {
	for _, n := range nodes {
		nm, _ := n.(map[string]any)
		if NodeIDString(nm["id"]) != nid {
			continue
		}
		name := fallbackName
		if inputs, ok := nm["inputs"].([]any); ok && slot >= 0 && slot < len(inputs) {
			if im, ok := inputs[slot].(map[string]any); ok {
				if s, _ := im["name"].(string); s != "" {
					name = s
				}
				im["link"] = nil
				inputs[slot] = im
			}
			nm["inputs"] = inputs
		}
		named, _ := nm["widgets_values_named"].(map[string]any)
		if named == nil {
			named = map[string]any{}
		}
		named[name] = value
		nm["widgets_values_named"] = named
		return
	}
}

func InstanceValues(node map[string]any) map[string]any {
	out := map[string]any{}
	if named, ok := node["widgets_values_named"].(map[string]any); ok {
		for k, v := range named {
			out[k] = v
		}
	}
	if len(out) > 0 {
		return out
	}
	arr, ok := node["widgets_values"].([]any)
	if !ok {
		return out
	}
	inputs, _ := node["inputs"].([]any)
	wi := 0
	for _, in := range inputs {
		im, _ := in.(map[string]any)
		if im == nil {
			continue
		}
		if im["widget"] == nil {
			continue
		}
		name, _ := im["name"].(string)
		if w, ok := im["widget"].(map[string]any); ok {
			if s, _ := w["name"].(string); s != "" {
				name = s
			}
		}
		for wi < len(arr) && IsControlWidget(arr[wi]) {
			wi++
		}
		if wi >= len(arr) {
			break
		}
		if name != "" {
			out[name] = arr[wi]
		}
		wi++
	}
	return out
}

func SubgraphDefs(root map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	defs, _ := root["definitions"].(map[string]any)
	if defs == nil {
		return out
	}
	switch sgs := defs["subgraphs"].(type) {
	case []any:
		for _, item := range sgs {
			m, _ := item.(map[string]any)
			id, _ := m["id"].(string)
			if id != "" {
				out[id] = m
			}
		}
	case map[string]any:
		for id, item := range sgs {
			if m, ok := item.(map[string]any); ok {
				out[id] = m
			}
		}
	}
	return out
}

func parseLinks(v any) []linkRec {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []linkRec
	for _, item := range arr {
		if l, ok := asLink(item); ok {
			out = append(out, l)
		}
	}
	return out
}

func asLink(item any) (linkRec, bool) {
	switch t := item.(type) {
	case []any:
		if len(t) < 5 {
			return linkRec{}, false
		}
		typ := ""
		if len(t) > 5 {
			typ = fmt.Sprint(t[5])
		}
		return linkRec{
			ID: int(asFloat(t[0])), OriginID: t[1], OriginSlot: int(asFloat(t[2])),
			TargetID: t[3], TargetSlot: int(asFloat(t[4])), Type: typ,
		}, true
	case map[string]any:
		return linkRec{
			ID: int(asFloat(t["id"])), OriginID: t["origin_id"], OriginSlot: int(asFloat(t["origin_slot"])),
			TargetID: t["target_id"], TargetSlot: int(asFloat(t["target_slot"])), Type: fmt.Sprint(t["type"]),
		}, true
	default:
		return linkRec{}, false
	}
}

func dumpLinks(links []linkRec) []any {
	out := make([]any, 0, len(links))
	for _, l := range links {
		out = append(out, []any{l.ID, l.OriginID, l.OriginSlot, l.TargetID, l.TargetSlot, l.Type})
	}
	return out
}

func leftoverUUIDNode(nodes any) string {
	arr, _ := nodes.([]any)
	for _, n := range arr {
		nm, _ := n.(map[string]any)
		typ, _ := nm["type"].(string)
		if CatalogUUID(typ) {
			return NodeIDString(nm["id"])
		}
	}
	return ""
}

func CatalogUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

func isIONode(id any) bool {
	n := int(asFloat(id))
	return n == subgraphInputNodeID || n == subgraphOutputNodeID
}

func NodeIDString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func asIntList(v any) []int {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []int
	for _, x := range arr {
		out = append(out, int(asFloat(x)))
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func IsControlWidget(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	switch s {
	case "fixed", "increment", "decrement", "randomize":
		return true
	default:
		return false
	}
}
