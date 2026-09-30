package catalogimport

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/comfy"
	"github.com/javded-itres/open-comfy/internal/importwf/convert"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// wanNode matches Wan video nodes (WanImageToVideo) without treating
// PreviewAny as video: its lowercased name contains the letters "wan".
func wanNode(low string) bool {
	return strings.HasPrefix(low, "wan") || strings.Contains(low, "wan2") || strings.Contains(low, "_wan")
}

func Slug(filename string) string {
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	s := strings.ToLower(base)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "workflow"
	}
	return s
}

func Infer(id, filename, display string, graph map[string]any, info map[string]comfy.NodeDef) (catalog.Model, error) {
	m := catalog.Model{
		ID:       id,
		Name:     display,
		Workflow: id + ".json",
		Created:  1726700000,
	}
	if m.Name == "" {
		m.Name = id
	}
	video := false
	var saves []string
	var displayOut []string
	for nid, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		low := strings.ToLower(ct)
		if strings.Contains(low, "save") || strings.HasPrefix(low, "vhs_") || strings.Contains(low, "createvideo") {
			saves = append(saves, nid)
			if strings.Contains(low, "video") || strings.HasPrefix(low, "vhs_") {
				video = true
			}
		}
		if def, ok := info[ct]; ok && def.OutputNode {
			displayOut = append(displayOut, nid)
		}
		if strings.Contains(low, "minimax") && strings.Contains(low, "video") {
			video = true
		}
		if strings.Contains(low, "ltx") || strings.Contains(low, "i2v") || wanNode(low) {
			video = true
		}
	}
	if video {
		m.Modality = "video"
		m.OutputMIME = "video/mp4"
		m.TimeoutS = 900
		m.Pricing = catalog.Pricing{Currency: "USD", PerSecond: 0.05}
	} else {
		m.Modality = "image"
		m.OutputMIME = "image/png"
		m.TimeoutS = 180
		m.Pricing = catalog.Pricing{Currency: "USD", PerImage: 0.02}
	}
	switch {
	case len(saves) == 1:
		m.OutputNode = saves[0]
	case len(saves) > 1:
		m.OutputNode = pickPref(saves, graph, video)
	case len(displayOut) > 0:
		m.OutputNode = displayOut[0]
	default:
		return m, fmt.Errorf("no output node (SaveImage/SaveVideo)")
	}

	m.Parameters = inferParams(graph, info)
	if m.Param("prompt") == nil {
		if p := findStringField(graph, "prompt"); p != nil {
			p.Required = true
			m.Parameters = append([]catalog.Param{*p}, m.Parameters...)
		}
	}
	if errs := Gate(&m, graph); len(errs) > 0 {
		return m, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return m, nil
}

func Gate(m *catalog.Model, graph map[string]any) []string {
	var errs []string
	if m.Param("prompt") == nil {
		errs = append(errs, "prompt not detected")
	}
	if needsReference(graph) && m.Param("input_image") == nil && m.Param("input_video") == nil {
		errs = append(errs, "reference (input image/video) not detected")
	}
	return errs
}

func needsReference(graph map[string]any) bool {
	for _, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct := strings.ToLower(fmt.Sprint(node["class_type"]))
		if ct == "loadimage" || ct == "loadvideo" || strings.Contains(ct, "loadimage") {
			return true
		}
		if strings.Contains(ct, "referencetovideo") {
			return true
		}
		inputs, _ := node["inputs"].(map[string]any)
		if inputs == nil {
			continue
		}
		if _, ok := inputs["ref_image"]; ok {
			return true
		}
		if _, ok := inputs["ref_images"]; ok {
			return true
		}
	}
	return false
}

func pickPref(ids []string, graph map[string]any, video bool) string {
	for _, id := range ids {
		ct, _ := graph[id].(map[string]any)["class_type"].(string)
		low := strings.ToLower(ct)
		if video && (strings.Contains(low, "video") || strings.HasPrefix(low, "vhs_")) {
			return id
		}
		if !video && strings.Contains(low, "image") {
			return id
		}
	}
	return ids[len(ids)-1]
}

func inferParams(graph map[string]any, info map[string]comfy.NodeDef) []catalog.Param {
	var out []catalog.Param
	add := func(p catalog.Param) {
		if p.Name == "" || len(p.MapsTo) == 0 {
			return
		}
		for i := range out {
			if out[i].Name == p.Name {
				out[i].MapsTo = append(out[i].MapsTo, p.MapsTo...)
				return
			}
		}
		out = append(out, p)
	}

	for nid, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		title := strings.ToLower(nodeTitle(node))
		inputs, _ := node["inputs"].(map[string]any)
		lowCT := strings.ToLower(ct)

		if hasScalar(inputs, "text") && !strings.Contains(lowCT, "markdown") && !strings.Contains(lowCT, "note") {
			if lowCT == "cliptextencode" || strings.Contains(lowCT, "textencode") || strings.Contains(lowCT, "minimax") || looksLikeUUID(lowCT) {
				name := "prompt"
				txt, _ := inputs["text"].(string)
				if strings.Contains(title, "negative") || strings.Contains(title, "neg") || strings.Contains(strings.ToLower(txt), "low quality") {
					name = "negative_prompt"
				}
				req := name == "prompt"
				add(catalog.Param{Name: name, Type: "string", Required: req, MapsTo: []catalog.MapTo{{Node: nid, Field: "text"}}})
			}
		}
		if hasScalar(inputs, "prompt") && (strings.Contains(lowCT, "minimax") || strings.Contains(lowCT, "flux") || strings.Contains(lowCT, "ltx") || strings.Contains(lowCT, "wan") || strings.Contains(lowCT, "text")) {
			add(catalog.Param{Name: "prompt", Type: "string", Required: true, MapsTo: []catalog.MapTo{{Node: nid, Field: "prompt"}}})
		}
		if (strings.Contains(lowCT, "primitivestring") || lowCT == "textmultiline") && hasScalar(inputs, "value") {
			if strings.Contains(title, "prompt") && !strings.Contains(title, "negative") {
				add(catalog.Param{Name: "prompt", Type: "string", Required: true, MapsTo: []catalog.MapTo{{Node: nid, Field: "value"}}})
			}
		}
		if hasScalar(inputs, "noise_seed") {
			add(catalog.Param{Name: "seed", Type: "integer", Default: -1, MapsTo: []catalog.MapTo{{Node: nid, Field: "noise_seed"}}})
		} else if hasScalar(inputs, "seed") && !strings.Contains(lowCT, "load") {
			add(catalog.Param{Name: "seed", Type: "integer", Default: -1, MapsTo: []catalog.MapTo{{Node: nid, Field: "seed"}}})
		}
		if hasScalar(inputs, "width") {
			add(catalog.Param{Name: "width", Type: "integer", Default: inputs["width"], MapsTo: []catalog.MapTo{{Node: nid, Field: "width"}}})
		}
		if hasScalar(inputs, "height") {
			add(catalog.Param{Name: "height", Type: "integer", Default: inputs["height"], MapsTo: []catalog.MapTo{{Node: nid, Field: "height"}}})
		}
		if hasScalar(inputs, "fps") {
			add(catalog.Param{Name: "fps", Type: "integer", Default: inputs["fps"], MapsTo: []catalog.MapTo{{Node: nid, Field: "fps"}}})
		}
		if (strings.Contains(title, "duration") || strings.Contains(title, "seconds")) && hasScalar(inputs, "value") {
			add(catalog.Param{Name: "seconds", Type: "number", Default: inputs["value"], MapsTo: []catalog.MapTo{{Node: nid, Field: "value"}}})
		}
		if hasScalar(inputs, "length") && strings.Contains(lowCT, "minimax") {
			exists := false
			for _, p := range out {
				if p.Name == "seconds" {
					exists = true
					break
				}
			}
			if !exists {
				add(catalog.Param{Name: "seconds", Type: "number", Default: 6, Transform: "frames_from_seconds", MapsTo: []catalog.MapTo{{Node: nid, Field: "length"}}})
			}
		}
		if lowCT == "loadimage" {
			if _, ok := inputs["image"]; ok {
				add(catalog.Param{Name: "input_image", Type: "image", Required: true, MapsTo: []catalog.MapTo{{Node: nid, Field: "image"}}})
			}
		}
		if lowCT == "loadvideo" {
			field := "image"
			if _, ok := inputs["file"]; ok {
				field = "file"
			}
			if _, ok := inputs[field]; ok {
				add(catalog.Param{Name: "input_video", Type: "image", Required: true, MapsTo: []catalog.MapTo{{Node: nid, Field: field}}})
			}
		}
	}
	return append(out, widgetParams(graph, info, out)...)
}

// widgetParams turns scalar node widgets into overridable parameters.
// The default is the value stored in the workflow, so a later request can
// replace max_length or sampling_mode.temperature without editing the graph.
func widgetParams(graph map[string]any, info map[string]comfy.NodeDef, have []catalog.Param) []catalog.Param {
	if len(info) == 0 {
		return nil
	}
	type hit struct {
		nid, class, field string
		val               any
		spec              any
	}
	var hits []hit
	ids := make([]string, 0, len(graph))
	for id := range graph {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, nid := range ids {
		node, _ := graph[nid].(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		inputs, _ := node["inputs"].(map[string]any)
		fields := make([]string, 0, len(inputs))
		for field := range inputs {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			if !hasScalar(inputs, field) || mapped(have, nid, field) {
				continue
			}
			if skipWidget(field, inputs[field]) {
				continue
			}
			spec := convert.FieldSpec(info[ct], field)
			if spec == nil {
				continue
			}
			hits = append(hits, hit{nid: nid, class: ct, field: field, val: inputs[field], spec: spec})
		}
	}
	byField := map[string][]hit{}
	var order []string
	for _, h := range hits {
		if _, ok := byField[h.field]; !ok {
			order = append(order, h.field)
		}
		byField[h.field] = append(byField[h.field], h)
	}
	var out []catalog.Param
	for _, field := range order {
		group := byField[field]
		same := true
		for _, h := range group[1:] {
			if fmt.Sprint(h.val) != fmt.Sprint(group[0].val) {
				same = false
				break
			}
		}
		if same && !nameTaken(have, field) && !nameTaken(out, field) {
			p := widgetParam(field, group[0].spec, group[0].val)
			for _, h := range group {
				p.MapsTo = append(p.MapsTo, catalog.MapTo{Node: h.nid, Field: h.field})
			}
			out = append(out, p)
			continue
		}
		for _, h := range group {
			name := field + "__" + sanitizeNode(h.nid)
			if nameTaken(have, name) || nameTaken(out, name) {
				name = h.class + "__" + name
			}
			p := widgetParam(name, h.spec, h.val)
			p.MapsTo = []catalog.MapTo{{Node: h.nid, Field: h.field}}
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func widgetParam(name string, spec, val any) catalog.Param {
	p := catalog.Param{Name: name, Type: widgetParamType(spec), Default: widgetDefaultValue(spec, val)}
	if min, ok := specNumber(spec, "min"); ok {
		p.Min = &min
	}
	if max, ok := specNumber(spec, "max"); ok {
		p.Max = &max
	}
	if enum := specEnum(spec); len(enum) > 0 {
		p.Enum = enum
	}
	return p
}

func widgetParamType(spec any) string {
	arr, _ := spec.([]any)
	if len(arr) == 0 {
		return "string"
	}
	switch t := arr[0].(type) {
	case []any:
		return "string"
	case string:
		switch strings.ToUpper(t) {
		case "INT":
			return "integer"
		case "FLOAT", "NUMBER":
			return "number"
		case "BOOLEAN":
			return "boolean"
		default:
			return "string"
		}
	default:
		return "string"
	}
}

func widgetDefaultValue(spec, val any) any {
	if widgetParamType(spec) == "integer" {
		switch n := val.(type) {
		case float64:
			if n == float64(int(n)) {
				return int(n)
			}
		case int:
			return n
		}
	}
	return val
}

func specNumber(spec any, key string) (float64, bool) {
	arr, ok := spec.([]any)
	if !ok || len(arr) < 2 {
		return 0, false
	}
	meta, ok := arr[1].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := meta[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func specEnum(spec any) []any {
	arr, ok := spec.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	if opts, ok := arr[0].([]any); ok {
		var out []any
		for _, o := range opts {
			if s, ok := o.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if len(arr) < 2 {
		return nil
	}
	meta, _ := arr[1].(map[string]any)
	options, _ := meta["options"].([]any)
	var out []any
	for _, opt := range options {
		om, _ := opt.(map[string]any)
		if key, _ := om["key"].(string); key != "" {
			out = append(out, key)
		}
	}
	return out
}

func mapped(ps []catalog.Param, nid, field string) bool {
	for _, p := range ps {
		for _, mt := range p.MapsTo {
			if mt.Node == nid && mt.Field == field {
				return true
			}
		}
	}
	return false
}

func nameTaken(ps []catalog.Param, name string) bool {
	for _, p := range ps {
		if p.Name == name {
			return true
		}
	}
	return false
}

func sanitizeNode(id string) string {
	r := strings.NewReplacer(":", "_", "/", "_", " ", "_")
	return r.Replace(id)
}

func skipWidget(field string, val any) bool {
	f := strings.ToLower(field)
	switch f {
	case "ckpt_name", "unet_name", "vae_name", "clip_name", "lora_name",
		"control_net_name", "controlnet_name", "model_name", "diffusion_model",
		"clip_name1", "clip_name2", "clip_name3", "t5_name", "text_encoder_name",
		"image", "images", "file":
		return true
	}
	s, _ := val.(string)
	if s == "" {
		return false
	}
	switch strings.ToLower(filepath.Ext(s)) {
	case ".safetensors", ".ckpt", ".pt", ".pth", ".bin", ".gguf", ".sft", ".onnx",
		".png", ".jpg", ".jpeg", ".webp", ".gif", ".mp4", ".webm", ".mov":
		return true
	}
	return false
}

func hasScalar(inputs map[string]any, field string) bool {
	if inputs == nil {
		return false
	}
	v, ok := inputs[field]
	if !ok || v == nil {
		return false
	}
	switch v.(type) {
	case []any:
		return false // link
	default:
		return true
	}
}

func nodeTitle(node map[string]any) string {
	if meta, ok := node["_meta"].(map[string]any); ok {
		if t, _ := meta["title"].(string); t != "" {
			return t
		}
	}
	return ""
}

func looksLikeUUID(s string) bool {
	return len(s) >= 36 && strings.Count(s, "-") >= 4
}

func findStringField(graph map[string]any, field string) *catalog.Param {
	for nid, v := range graph {
		node, _ := v.(map[string]any)
		inputs, _ := node["inputs"].(map[string]any)
		if hasScalar(inputs, field) {
			return &catalog.Param{Name: field, Type: "string", Required: false, MapsTo: []catalog.MapTo{{Node: nid, Field: field}}}
		}
	}
	return nil
}
