package importwf

import (
	"encoding/json"
	"path"
	"strconv"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
)

type Analysis struct {
	Name            string      `json:"name,omitempty"`
	Format          string      `json:"format"`
	MissingNodes    []NodeNeed  `json:"missing_nodes"`
	MissingModels   []ModelNeed `json:"missing_models"`
	ReusedModels    []ModelNeed `json:"reused_models,omitempty"`
	Allowlisted     []NodeNeed  `json:"allowlisted_packs"`
	Blocked         []NodeNeed  `json:"blocked_packs"`
	Ready           bool        `json:"ready"`
	CanInstallNodes bool        `json:"can_install_nodes"`
	Errors          []string    `json:"errors,omitempty"`
}

type NodeNeed struct {
	Class       string `json:"class_type"`
	Pack        string `json:"pack,omitempty"`
	Git         string `json:"git,omitempty"`
	Allowlisted bool   `json:"allowlisted"`
	Installed   bool   `json:"installed"`
}

type ModelNeed struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Node  string `json:"node"`
	Class string `json:"class_type"`
	Local string `json:"local,omitempty"`
}

func Analyze(raw json.RawMessage, info map[string]comfy.NodeDef, allow []string, modelsDir string) Analysis {
	a := Analysis{Format: "unknown"}
	if len(raw) == 0 {
		a.Errors = append(a.Errors, "empty workflow")
		return a
	}
	classes, models := collectDeps(raw, info)
	if isUIWorkflow(raw) {
		a.Format = "ui"
	} else if g, err := catalog.UnwrapRaw(raw); err == nil {
		a.Format = "api"
		_ = g
	}
	seenClass := map[string]bool{}
	packSeen := map[string]*NodeNeed{}
	for _, ct := range classes {
		if seenClass[ct] {
			continue
		}
		seenClass[ct] = true
		if skipClass(ct) {
			continue
		}
		need := NodeNeed{Class: ct, Installed: infoHas(info, ct)}
		if !need.Installed {
			if p, git, ok := LookupPack(ct); ok {
				need.Pack, need.Git = p, git
				need.Allowlisted = PackAllowed(git, allow)
			}
			a.MissingNodes = append(a.MissingNodes, need)
			if need.Git != "" {
				key := strings.ToLower(need.Git)
				if packSeen[key] == nil {
					cp := need
					packSeen[key] = &cp
					if need.Allowlisted {
						a.Allowlisted = append(a.Allowlisted, need)
					} else {
						a.Blocked = append(a.Blocked, need)
					}
				}
			} else {
				a.Blocked = append(a.Blocked, need)
			}
		}
	}
	seenM := map[string]bool{}
	for _, m := range models {
		m.Value = normalizeModelPath(m.Value)
		if m.Value == "" {
			continue
		}
		key := m.Field + "\t" + m.Value
		if seenM[key] {
			continue
		}
		seenM[key] = true
		opts := comboOptions(info, m.Class, m.Field)
		if len(opts) == 0 && !looksLikeWeightFile(m.Value) {
			continue
		}
		if len(opts) > 0 && comboHas(opts, m.Value) {
			continue
		}
		if modelsDir != "" {
			if found, dest, ok := reuseLocalModel(modelsDir, m.Class, m.Field, m.Value); ok {
				m.Local = found
				if dest != "" && dest != found {
					m.Local = found + " → " + dest
				}
				a.ReusedModels = append(a.ReusedModels, m)
				continue
			}
		}
		a.MissingModels = append(a.MissingModels, m)
	}
	a.Ready = len(a.MissingNodes) == 0 && len(a.MissingModels) == 0
	a.CanInstallNodes = len(a.Allowlisted) > 0
	return a
}

func infoHas(info map[string]comfy.NodeDef, ct string) bool {
	_, ok := info[ct]
	return ok
}

func skipClass(ct string) bool {
	switch ct {
	case "", "Note", "MarkdownNote", "Reroute":
		return true
	}
	return catalog.IsUUIDClass(ct)
}

func collectDeps(raw json.RawMessage, info map[string]comfy.NodeDef) (classes []string, models []ModelNeed) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, nil
	}
	if nodes, ok := root["nodes"].([]any); ok {
		defs := subgraphDefs(root)
		collectUINodes(nodes, defs, "", info, &classes, &models)
		return classes, models
	}
	for id, v := range root {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if ct == "" {
			continue
		}
		classes = append(classes, ct)
		inputs, _ := node["inputs"].(map[string]any)
		for field, val := range inputs {
			s, ok := val.(string)
			if !ok || s == "" || !looksLikeModelField(field) {
				continue
			}
			models = append(models, ModelNeed{Field: field, Value: s, Node: id, Class: ct})
		}
	}
	return classes, models
}

func collectUINodes(nodes []any, defs map[string]map[string]any, prefix string, info map[string]comfy.NodeDef, classes *[]string, models *[]ModelNeed) {
	for _, n := range nodes {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := nm["type"].(string)
		id := nodeIDString(nm["id"])
		if prefix != "" {
			id = prefix + ":" + id
		}
		if def, ok := defs[ct]; ok && catalogUUID(ct) {
			inner, _ := def["nodes"].([]any)
			collectUINodes(inner, defs, id, info, classes, models)
			continue
		}
		*classes = append(*classes, ct)
		var def comfy.NodeDef
		if info != nil {
			def = info[ct]
		}
		vals := instanceValuesDef(nm, def)
		seen := map[string]bool{}
		for field, val := range vals {
			s, ok := val.(string)
			if !ok || s == "" {
				continue
			}
			if !looksLikeModelField(field) && !looksLikeWeightFile(s) {
				continue
			}
			*models = append(*models, ModelNeed{Field: field, Value: s, Node: id, Class: ct})
			seen[strings.ToLower(path.Base(normalizeModelPath(s)))] = true
		}
		i := 0
		for _, s := range widgetStringList(nm) {
			if !looksLikeWeightFile(s) {
				continue
			}
			base := strings.ToLower(path.Base(normalizeModelPath(s)))
			if seen[base] {
				continue
			}
			seen[base] = true
			*models = append(*models, ModelNeed{Field: guessModelField(ct, i), Value: s, Node: id, Class: ct})
			i++
		}
	}
}

func instanceValuesDef(node map[string]any, def comfy.NodeDef) map[string]any {
	out := instanceValues(node)
	if len(out) > 0 {
		return out
	}
	names := widgetNames(def)
	if len(names) == 0 {
		return out
	}
	arr, ok := node["widgets_values"].([]any)
	if !ok {
		return out
	}
	wi := 0
	for _, name := range names {
		for wi < len(arr) && isControlWidget(arr[wi]) {
			wi++
		}
		if wi >= len(arr) {
			break
		}
		out[name] = arr[wi]
		wi++
	}
	return out
}

func widgetStringList(node map[string]any) []string {
	arr, _ := node["widgets_values"].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func guessModelField(class string, i int) string {
	c := strings.ToLower(class)
	switch {
	case strings.Contains(c, "dualclip") || strings.Contains(c, "tripleclip"):
		return "clip_name" + strconv.Itoa(i+1)
	case strings.Contains(c, "unet") || strings.Contains(c, "diffusion"):
		return "unet_name"
	case strings.Contains(c, "vae"):
		return "vae_name"
	case strings.Contains(c, "lora"):
		return "lora_name"
	case strings.Contains(c, "clip"):
		return "clip_name"
	default:
		return "ckpt_name"
	}
}

func looksLikeWeightFile(v string) bool {
	switch strings.ToLower(path.Ext(normalizeModelPath(v))) {
	case ".safetensors", ".ckpt", ".pt", ".pth", ".bin", ".gguf", ".sft", ".onnx":
		return true
	}
	return false
}

func looksLikeModelField(field string) bool {
	f := strings.ToLower(field)
	switch f {
	case "ckpt_name", "unet_name", "vae_name", "clip_name", "lora_name",
		"control_net_name", "controlnet_name", "model_name", "diffusion_model",
		"clip_name1", "clip_name2", "clip_name3", "t5_name", "text_encoder_name":
		return true
	}
	return strings.HasSuffix(f, "_name") && (strings.Contains(f, "ckpt") ||
		strings.Contains(f, "unet") || strings.Contains(f, "vae") ||
		strings.Contains(f, "clip") || strings.Contains(f, "lora") ||
		strings.Contains(f, "model"))
}

func comboOptions(info map[string]comfy.NodeDef, class, field string) []string {
	def, ok := info[class]
	if !ok {
		return nil
	}
	for _, group := range []string{"required", "optional"} {
		spec, ok := def.Input[group][field]
		if !ok {
			continue
		}
		return comboFromSpec(spec)
	}
	return nil
}

func comboFromSpec(spec any) []string {
	arr, ok := spec.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	switch t := arr[0].(type) {
	case []any:
		return stringList(t)
	case string:
		if strings.EqualFold(t, "COMBO") && len(arr) > 1 {
			if m, ok := arr[1].(map[string]any); ok {
				return stringList(m["options"])
			}
		}
	}
	return nil
}

func stringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range arr {
		if s, ok := it.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func comboHas(opts []string, val string) bool {
	for _, o := range opts {
		if o == val {
			return true
		}
	}
	base := val
	if i := strings.LastIndex(val, "/"); i >= 0 {
		base = val[i+1:]
	}
	for _, o := range opts {
		if o == base || strings.HasSuffix(o, "/"+base) {
			return true
		}
	}
	return false
}
