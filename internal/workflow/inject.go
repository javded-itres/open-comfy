package workflow

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/javded-itres/open-comfy/internal/catalog"
)

var sizeWxH = regexp.MustCompile(`^(\d+)x(\d+)$`)

type Request struct {
	Model          string
	Prompt         string
	NegativePrompt string
	N              int
	Size           string
	Quality        string
	ResponseFormat string
	Width          *int
	Height         *int
	Seed           *int64
	Seconds        any
	Duration       any
	FPS            *int
	Resolution     string
	AspectRatio    string
	Extra          map[string]any
	InputImage     []byte
	InputImages    [][]byte
	InputName      string // already a comfy filename
	InputNames     []string
	HasInputImage  bool
}

func (r Request) ImageBlobs() [][]byte {
	if len(r.InputImages) > 0 {
		return r.InputImages
	}
	if len(r.InputImage) > 0 {
		return [][]byte{r.InputImage}
	}
	return nil
}

func (r Request) ImageFiles() []string {
	if len(r.InputNames) > 0 {
		return r.InputNames
	}
	if r.InputName != "" {
		return []string{r.InputName}
	}
	return nil
}

func DeepCopy(graph map[string]any) (map[string]any, error) {
	b, err := json.Marshal(graph)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func BuildValues(cat *catalog.Catalog, m *catalog.Model, req Request) (map[string]any, error) {
	values := map[string]any{}
	for _, p := range m.Parameters {
		if p.Default != nil {
			values[p.Name] = p.Default
		}
	}
	if req.Quality != "" && m.QualityMap != nil {
		if q, ok := m.QualityMap[req.Quality]; ok {
			for k, v := range q {
				if _, client := req.Extra[k]; client {
					continue
				}
				if explicitParam(req, k) {
					continue
				}
				values[k] = v
			}
		}
	}
	if req.Prompt != "" {
		values["prompt"] = req.Prompt
	}
	if req.NegativePrompt != "" {
		values["negative_prompt"] = req.NegativePrompt
	}
	if req.Seed != nil {
		values["seed"] = *req.Seed
	}
	if req.FPS != nil {
		values["fps"] = *req.FPS
	}
	if req.Seconds != nil {
		values["seconds"] = req.Seconds
	} else if req.Duration != nil {
		values["seconds"] = req.Duration
	}
	if req.N > 0 {
		values["n"] = req.N
	}
	for k, v := range req.Extra {
		if k == "duration" {
			values["seconds"] = v
			continue
		}
		if k == "input_reference" || k == "input_references" || k == "input_image" || k == "input_images" {
			continue
		}
		if k == "prompt" || k == "negative_prompt" {
			continue
		}
		values[k] = v
	}
	if names := req.ImageFiles(); len(names) == 1 {
		values["input_image"] = names[0]
		req.HasInputImage = true
	} else if len(names) > 1 {
		values["input_image"] = names
		req.HasInputImage = true
	} else if req.HasInputImage {
		values["input_image"] = req.InputImage
	}

	if err := applySize(cat, m, req, values); err != nil {
		return nil, err
	}

	for _, p := range m.Parameters {
		if !p.IsOverridable() {
			if clientSent(req, values, p.Name) && !sameDefault(p.Default, values[p.Name]) {
				return nil, lockedErr(p.Name)
			}
		}
		if p.Required {
			pv := values[p.Name]
			if pv == nil || pv == "" {
				if p.Type == "image" && !req.HasInputImage && req.InputName == "" && len(req.InputNames) == 0 && len(req.InputImages) == 0 {
					return nil, missingErr(p.Name)
				}
				if p.Type != "image" {
					return nil, missingErr(p.Name)
				}
			}
		}
		if v, ok := values[p.Name]; ok {
			cv, err := coerce(p, v)
			if err != nil {
				return nil, err
			}
			values[p.Name] = cv
		}
	}

	for _, p := range m.Parameters {
		switch p.Transform {
		case "frames_from_seconds":
			sec := asFloat(values["seconds"])
			fps := asFloat(values["fps"])
			if fps == 0 {
				fps = 24
			}
			values[p.Name] = int(math.Round(sec * fps))
		case "longer_side":
			w := asFloat(values["width"])
			h := asFloat(values["height"])
			values[p.Name] = int(math.Max(w, h))
		}
	}

	if v, ok := values["seed"]; ok {
		values["seed"] = normalizeSeed(v)
	} else if m.Param("seed") != nil {
		values["seed"] = randomSeed()
	}
	if s, ok := values["prompt"].(string); ok {
		values["prompt"] = CleanChatPrompt(s)
	}
	if s, ok := values["negative_prompt"].(string); ok {
		values["negative_prompt"] = CleanChatPrompt(s)
	}
	return values, nil
}

func explicitParam(req Request, k string) bool {
	switch k {
	case "steps":
		_, ok := req.Extra["steps"]
		return ok
	case "cfg_scale":
		_, ok := req.Extra["cfg_scale"]
		return ok
	}
	return false
}

func clientSent(req Request, values map[string]any, name string) bool {
	if _, ok := req.Extra[name]; ok {
		return true
	}
	switch name {
	case "prompt":
		return req.Prompt != ""
	case "negative_prompt":
		return req.NegativePrompt != ""
	case "seed":
		return req.Seed != nil
	case "seconds":
		return req.Seconds != nil || req.Duration != nil
	}
	return false
}

func sameDefault(def, v any) bool {
	return fmt.Sprint(def) == fmt.Sprint(v)
}

func applySize(cat *catalog.Catalog, m *catalog.Model, req Request, values map[string]any) error {
	if req.Width != nil && req.Height != nil {
		values["width"] = *req.Width
		values["height"] = *req.Height
		if req.AspectRatio != "" {
			wh, ok := cat.LookupSize(m, req.AspectRatio)
			if ok {
				got := float64(*req.Width) / float64(*req.Height)
				want := float64(wh.Width) / float64(wh.Height)
				if math.Abs(got-want)/want > 0.01 {
					return invalidErr("size", "size conflicts with aspect_ratio")
				}
			}
		}
		return nil
	}
	key := req.Size
	if key == "" {
		key = req.Resolution
	}
	if key == "" && req.AspectRatio != "" {
		key = req.AspectRatio
	}
	if key == "" {
		return nil
	}
	if wh, ok := cat.LookupSize(m, key); ok {
		if req.AspectRatio != "" && key != req.AspectRatio {
			awh, ok2 := cat.LookupSize(m, req.AspectRatio)
			if ok2 {
				got := float64(wh.Width) / float64(wh.Height)
				want := float64(awh.Width) / float64(awh.Height)
				if math.Abs(got-want)/want > 0.01 {
					values["width"] = awh.Width
					values["height"] = awh.Height
					return nil
				}
			}
		}
		values["width"] = wh.Width
		values["height"] = wh.Height
		return nil
	}
	if mm := sizeWxH.FindStringSubmatch(key); mm != nil {
		w, _ := strconv.Atoi(mm[1])
		h, _ := strconv.Atoi(mm[2])
		if req.AspectRatio != "" {
			awh, ok := cat.LookupSize(m, req.AspectRatio)
			if ok {
				got := float64(w) / float64(h)
				want := float64(awh.Width) / float64(awh.Height)
				if math.Abs(got-want)/want > 0.01 {
					return invalidErr("size", "size conflicts with aspect_ratio")
				}
			}
		}
		values["width"] = w
		values["height"] = h
		return nil
	}
	return invalidErr("size", "unknown size "+key)
}

func Inject(graph map[string]any, m *catalog.Model, values map[string]any) (map[string]any, error) {
	g, err := DeepCopy(graph)
	if err != nil {
		return nil, err
	}
	for _, p := range m.Parameters {
		v, ok := values[p.Name]
		if !ok {
			continue
		}
		if p.Type == "image" && (v == nil || v == "") {
			continue
		}
		vals := expandImageValues(v)
		for i, mt := range p.MapsTo {
			node, ok := g[mt.Node].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("node %s missing", mt.Node)
			}
			inputs, ok := node["inputs"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("node %s inputs not map", mt.Node)
			}
			one := v
			if len(vals) > 0 {
				if i < len(vals) {
					one = vals[i]
				} else {
					one = vals[len(vals)-1]
				}
			}
			if err := writePath(inputs, mt.Field, mt.Path, one); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

func expandImageValues(v any) []any {
	switch t := v.(type) {
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case []any:
		return t
	default:
		return nil
	}
}

func writePath(inputs map[string]any, field string, path []string, val any) error {
	if len(path) == 0 {
		if _, ok := inputs[field]; !ok {
			return fmt.Errorf("field %s missing", field)
		}
		inputs[field] = val
		return nil
	}
	cur, ok := inputs[field]
	if !ok {
		return fmt.Errorf("field %s missing", field)
	}
	m, ok := asMap(cur)
	if !ok {
		return fmt.Errorf("field %s not a map", field)
	}
	inputs[field] = m
	for i, p := range path {
		if i == len(path)-1 {
			m[p] = val
			return nil
		}
		next, ok := m[p]
		if !ok {
			return fmt.Errorf("path %s missing", p)
		}
		nm, ok := asMap(next)
		if !ok {
			return fmt.Errorf("path %s not a map", p)
		}
		m[p] = nm
		m = nm
	}
	return nil
}

func asMap(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, true
	default:
		return nil, false
	}
}

func coerce(p catalog.Param, v any) (any, error) {
	switch p.Type {
	case "integer":
		n := int(asFloat(v))
		if p.Min != nil && float64(n) < *p.Min {
			return nil, invalidErr(p.Name, "below min")
		}
		if p.Max != nil && float64(n) > *p.Max {
			return nil, invalidErr(p.Name, "above max")
		}
		if err := checkEnum(p, n); err != nil {
			return nil, err
		}
		return n, nil
	case "number":
		f := asFloat(v)
		if p.Min != nil && f < *p.Min {
			return nil, invalidErr(p.Name, "below min")
		}
		if p.Max != nil && f > *p.Max {
			return nil, invalidErr(p.Name, "above max")
		}
		return f, nil
	case "string":
		s := fmt.Sprint(v)
		if err := checkEnum(p, s); err != nil {
			return nil, err
		}
		return s, nil
	case "image":
		return v, nil
	default:
		return v, nil
	}
}

func checkEnum(p catalog.Param, v any) error {
	if len(p.Enum) == 0 {
		return nil
	}
	sv := fmt.Sprint(v)
	for _, e := range p.Enum {
		if fmt.Sprint(e) == sv || fmt.Sprint(int(asFloat(e))) == sv {
			return nil
		}
	}
	return invalidErr(p.Name, "not in enum")
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func normalizeSeed(v any) int64 {
	n := int64(asFloat(v))
	if n <= 0 {
		return randomSeed()
	}
	return n
}

func randomSeed() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	n := int64(binary.BigEndian.Uint64(b[:]) & 0x7fffffff)
	if n == 0 {
		n = 1
	}
	return n
}

type Error struct {
	Code    string
	Param   string
	Message string
}

func (e *Error) Error() string { return e.Message }

func missingErr(param string) error {
	msg := "missing required parameter " + param
	if param == "input_image" || param == "input_video" {
		msg += "; send a data URL in input_image, input_images, input_reference, or input_references"
	}
	return &Error{Code: "invalid_prompt", Param: param, Message: msg}
}
func invalidErr(param, msg string) error {
	return &Error{Code: "invalid_value", Param: param, Message: msg}
}
func lockedErr(param string) error {
	return &Error{Code: "param_locked", Param: param, Message: "parameter is locked: " + param}
}

func SecondsString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	n := int(asFloat(v))
	if n == 0 {
		return fmt.Sprint(v)
	}
	return strconv.Itoa(n)
}

func SizeString(values map[string]any) string {
	w := int(asFloat(values["width"]))
	h := int(asFloat(values["height"]))
	if w == 0 || h == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", w, h)
}

// CleanChatPrompt drops MikroLLM/LiteLLM playground wrappers so T2I models
// (Z-Image etc.) do not render "Original image task…", assistant errors, or
// JSON settings as text in the picture.
func CleanChatPrompt(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if !strings.Contains(s, "Original image task") && !strings.Contains(s, "\nUser: ") && !strings.Contains(s, "\nAssistant:") {
		return s
	}
	var last string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "User: "):
			last = strings.TrimPrefix(line, "User: ")
		case strings.HasPrefix(line, "Assistant:"):
			continue
		}
	}
	if last != "" {
		return last
	}
	return s
}

func IgnoreOpenAI(k string) bool {
	switch k {
	case "style", "moderation", "user", "background", "partial_images",
		"output_compression", "stream", "model", "n", "response_format",
		"output_format", "extra_body", "extra", "wait", "modalities", "messages",
		"prompt", "negative_prompt",
		"input_reference", "input_references", "input_image", "input_images":
		return true
	}
	return strings.HasPrefix(k, "_")
}
