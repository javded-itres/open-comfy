package catalog

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Catalog struct {
	DefaultImage string            `yaml:"default_image"`
	DefaultVideo string            `yaml:"default_video"`
	SizeAliases  map[string]WH     `yaml:"size_aliases"`
	Models       []Model           `yaml:"models"`
	byID         map[string]*Model `yaml:"-"`
	alias        map[string]string `yaml:"-"`
	WorkflowsDir string            `yaml:"-"`
}

type WH struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

type Model struct {
	ID            string                    `yaml:"id"`
	Name          string                    `yaml:"name"`
	Aliases       []string                  `yaml:"aliases"`
	Modality      string                    `yaml:"modality"` // image | video
	Workflow      string                    `yaml:"workflow"`
	OutputMIME    string                    `yaml:"output_mime"`
	OutputNode    string                    `yaml:"output_node"`
	TimeoutS      int                       `yaml:"timeout_s"`
	MaxConcurrent int                       `yaml:"max_concurrent"`
	Pricing       Pricing                   `yaml:"pricing"`
	QualityMap    map[string]map[string]any `yaml:"quality_map"`
	Locked        []string                  `yaml:"locked"`
	SizeAliases   map[string]WH             `yaml:"size_aliases"`
	Parameters    []Param                   `yaml:"parameters"`
	Created       int64                     `yaml:"created"`
}

type Pricing struct {
	Currency  string  `yaml:"currency"`
	PerImage  float64 `yaml:"per_image"`
	PerSecond float64 `yaml:"per_second"`
}

type Param struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required"`
	Overridable *bool    `yaml:"overridable"`
	Default     any      `yaml:"default"`
	Min         *float64 `yaml:"min"`
	Max         *float64 `yaml:"max"`
	Enum        []any    `yaml:"enum"`
	MapsTo      []MapTo  `yaml:"maps_to"`
	Transform   string   `yaml:"transform"`
}

type MapTo struct {
	Node  string   `yaml:"node"`
	Field string   `yaml:"field"`
	Path  []string `yaml:"path"`
}

func (p Param) IsOverridable() bool {
	if p.Overridable == nil {
		return true
	}
	return *p.Overridable
}

func Load(path, workflowsDir string, skipCheck bool) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Catalog{WorkflowsDir: workflowsDir}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.SizeAliases == nil {
		c.SizeAliases = map[string]WH{}
	}
	all := c.Models
	c.Models = nil
	c.byID = map[string]*Model{}
	c.alias = map[string]string{}
	var skipped []string
	for i := range all {
		m := all[i]
		if m.TimeoutS <= 0 {
			if m.Modality == "video" {
				m.TimeoutS = 900
			} else {
				m.TimeoutS = 180
			}
		}
		if m.MaxConcurrent <= 0 {
			m.MaxConcurrent = 1
		}
		if m.Created == 0 {
			m.Created = 1726700000
		}
		if !skipCheck {
			if err := ValidateModel(&m, workflowsDir); err != nil {
				skipped = append(skipped, m.ID+": "+err.Error())
				log.Printf("opencomfy skip model %s: %v", m.ID, err)
				continue
			}
		}
		c.Models = append(c.Models, m)
		mm := &c.Models[len(c.Models)-1]
		c.byID[mm.ID] = mm
		c.alias[strings.ToLower(mm.ID)] = mm.ID
		for _, a := range mm.Aliases {
			c.alias[strings.ToLower(a)] = mm.ID
		}
	}
	if len(c.Models) == 0 && len(all) > 0 && !skipCheck {
		return nil, fmt.Errorf("no valid models in %s (%s)", path, strings.Join(skipped, "; "))
	}
	return c, nil
}

func NormalizeID(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, p := range []string{"openai/", "opencomfy/"} {
		if strings.HasPrefix(raw, p) {
			raw = strings.TrimPrefix(raw, p)
			break
		}
	}
	return raw
}

func (c *Catalog) Resolve(raw, modality string) (*Model, error) {
	id := NormalizeID(raw)
	if id == "" {
		if modality == "video" {
			id = c.DefaultVideo
		} else {
			id = c.DefaultImage
		}
	}
	if canon, ok := c.alias[strings.ToLower(id)]; ok {
		id = canon
	}
	m := c.byID[id]
	if m == nil {
		return nil, fmt.Errorf("unknown model %q", raw)
	}
	if modality != "" && m.Modality != modality {
		return nil, fmt.Errorf("model %q is %s, expected %s", m.ID, m.Modality, modality)
	}
	return m, nil
}

func (c *Catalog) Get(id string) *Model {
	return c.byID[id]
}

func (c *Catalog) LookupSize(m *Model, key string) (WH, bool) {
	if m != nil && m.SizeAliases != nil {
		if wh, ok := m.SizeAliases[key]; ok {
			return wh, true
		}
	}
	wh, ok := c.SizeAliases[key]
	return wh, ok
}

func WorkflowPath(dir, name string) (string, error) {
	base := filepath.Base(name)
	if base != name && !strings.HasPrefix(filepath.Clean(name), filepath.Clean(dir)+string(os.PathSeparator)) {
		// allow only basename
		name = base
	} else {
		name = base
	}
	p := filepath.Join(dir, name)
	if !strings.HasPrefix(filepath.Clean(p), filepath.Clean(dir)+string(os.PathSeparator)) && filepath.Clean(p) != filepath.Clean(dir) {
		return "", fmt.Errorf("workflow path escapes workflows_dir")
	}
	return p, nil
}

func LoadGraph(dir, name string) (map[string]any, error) {
	p, err := WorkflowPath(dir, name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return unwrapGraph(raw)
}

func UnwrapRaw(b []byte) (map[string]any, error) {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return unwrapGraph(raw)
}

func FirstUUIDClass(g map[string]any) string {
	for id, v := range g {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if IsUUIDClass(ct) {
			if id != "" {
				return id
			}
			return ct
		}
	}
	return ""
}

func IsUUIDClass(s string) bool {
	if len(s) != 36 {
		return false
	}
	return s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

func unwrapGraph(raw any) (map[string]any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("workflow is not an object")
	}
	if _, hasNodes := m["nodes"]; hasNodes {
		if _, hasLinks := m["links"]; hasLinks {
			return nil, fmt.Errorf("UI-format workflow (nodes/links); export API format")
		}
	}
	if prompt, ok := m["prompt"].(map[string]any); ok {
		m = prompt
	}
	for _, v := range m {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := node["class_type"]; ok {
			return m, nil
		}
	}
	return nil, fmt.Errorf("workflow missing class_type (not API format)")
}

func ValidateModel(m *Model, dir string) error {
	g, err := LoadGraph(dir, m.Workflow)
	if err != nil {
		return err
	}
	saves := 0
	for id, v := range g {
		node, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := node["class_type"].(string)
		if isSaveLike(ct) {
			saves++
			_ = id
		}
	}
	if m.OutputNode != "" {
		if _, ok := g[m.OutputNode]; !ok {
			return fmt.Errorf("output_node %q missing", m.OutputNode)
		}
	} else if saves == 0 {
		return fmt.Errorf("no Save*/VHS* output node")
	}
	if saves > 1 && m.OutputNode == "" {
		return fmt.Errorf("multiple save nodes require output_node")
	}
	for _, p := range m.Parameters {
		for _, mt := range p.MapsTo {
			node, ok := g[mt.Node].(map[string]any)
			if !ok {
				return fmt.Errorf("param %s maps_to node %q missing", p.Name, mt.Node)
			}
			inputs, ok := node["inputs"].(map[string]any)
			if !ok {
				return fmt.Errorf("param %s node %q inputs not a map", p.Name, mt.Node)
			}
			if _, ok := inputs[mt.Field]; !ok {
				return fmt.Errorf("param %s node %q missing field %q", p.Name, mt.Node, mt.Field)
			}
			if err := checkPath(inputs, mt.Field, mt.Path); err != nil {
				return fmt.Errorf("param %s: %w", p.Name, err)
			}
		}
	}
	return nil
}

func checkPath(inputs map[string]any, field string, path []string) error {
	if len(path) == 0 {
		return nil
	}
	cur, ok := inputs[field]
	if !ok {
		return fmt.Errorf("field %s missing", field)
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return fmt.Errorf("field %s is not a map for path", field)
	}
	for i, p := range path {
		if i == len(path)-1 {
			if _, ok := m[p]; !ok {
				return fmt.Errorf("path %s missing under %s", p, field)
			}
			return nil
		}
		next, ok := m[p]
		if !ok {
			return fmt.Errorf("path %s missing under %s", p, field)
		}
		m, ok = next.(map[string]any)
		if !ok {
			return fmt.Errorf("path %s is not a map", p)
		}
	}
	return nil
}

func isSaveLike(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "save") || strings.HasPrefix(ct, "vhs_")
}

// EnsureSaver attaches SaveImage/SaveVideo when the graph only has a UI
// output_node (e.g. FluxKleinOneNode). ComfyUI API skips nodes that do not
// feed a save, which yields empty history and no_output.
func EnsureSaver(graph map[string]any, modality, source string) (map[string]any, string) {
	if graph == nil {
		return graph, source
	}
	var existing string
	for id, v := range graph {
		node, _ := v.(map[string]any)
		if node == nil {
			continue
		}
		ct, _ := node["class_type"].(string)
		if isSaveLike(ct) {
			existing = id
			break
		}
	}
	if existing != "" {
		if source == "" {
			return graph, existing
		}
		return graph, source
	}
	if source == "" {
		return graph, source
	}
	if _, ok := graph[source]; !ok {
		return graph, source
	}
	g, err := deepcopyGraph(graph)
	if err != nil {
		return graph, source
	}
	id := "oc_save"
	for g[id] != nil {
		id += "x"
	}
	class, field := "SaveImage", "images"
	if modality == "video" {
		class, field = "SaveVideo", "images"
	}
	g[id] = map[string]any{
		"class_type": class,
		"inputs": map[string]any{
			"filename_prefix": "opencomfy",
			field:             []any{source, 0},
		},
	}
	return g, id
}

func deepcopyGraph(graph map[string]any) (map[string]any, error) {
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

func (m *Model) Param(name string) *Param {
	for i := range m.Parameters {
		if m.Parameters[i].Name == name {
			return &m.Parameters[i]
		}
	}
	return nil
}

func (m *Model) PublicParams() []map[string]any {
	out := make([]map[string]any, 0, len(m.Parameters))
	for _, p := range m.Parameters {
		item := map[string]any{
			"name":     p.Name,
			"type":     p.Type,
			"required": p.Required,
		}
		if p.Default != nil {
			item["default"] = p.Default
		}
		if p.Min != nil {
			item["min"] = *p.Min
		}
		if p.Max != nil {
			item["max"] = *p.Max
		}
		if len(p.Enum) > 0 {
			item["enum"] = p.Enum
		}
		out = append(out, item)
	}
	return out
}

func (m *Model) RequiredNames() []string {
	var out []string
	for _, p := range m.Parameters {
		if p.Required {
			out = append(out, p.Name)
		}
	}
	return out
}

// ToolName is a stable MCP tool id: generate_<model-id> with unsafe runes as '_'.
func (m *Model) ToolName() string {
	id := strings.ToLower(strings.TrimSpace(m.ID))
	var b strings.Builder
	b.WriteString("generate_")
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (m *Model) InputSchema() map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range m.Parameters {
		props[p.Name] = ParamJSONSchema(p)
		if p.Required {
			required = append(required, p.Name)
		}
		if p.Type == "image" && p.Name == "input_image" {
			if _, ok := props["input_images"]; !ok {
				props["input_images"] = map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
					"description": "Several data URLs; order matches LoadImage maps_to. Alias of input_image. " +
						"Also accepted: input_reference, input_references.",
				}
			}
		}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func ParamJSONSchema(p Param) map[string]any {
	item := map[string]any{}
	switch p.Type {
	case "image":
		item["type"] = "string"
		item["description"] = "Image as a data URL (data:image/png;base64,...). Aliases: input_images, input_reference, input_references."
	case "integer", "int":
		item["type"] = "integer"
	case "number", "float":
		item["type"] = "number"
	case "boolean", "bool":
		item["type"] = "boolean"
	default:
		item["type"] = "string"
	}
	if p.Default != nil {
		item["default"] = p.Default
	}
	if p.Min != nil {
		item["minimum"] = *p.Min
	}
	if p.Max != nil {
		item["maximum"] = *p.Max
	}
	if len(p.Enum) > 0 {
		item["enum"] = p.Enum
	}
	return item
}

func (m *Model) SupportedNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add("prompt")
	add("model")
	if m.Modality == "image" {
		add("size")
		add("n")
		add("response_format")
		add("quality")
	} else {
		add("seconds")
		add("size")
	}
	for _, p := range m.Parameters {
		add(p.Name)
	}
	return out
}

func (c *Catalog) All() []Model { return c.Models }

func (c *Catalog) Has(id string) bool {
	return c.byID[id] != nil
}

func (c *Catalog) Add(m Model) {
	c.Models = append(c.Models, m)
	c.reindex()
}

func (c *Catalog) Remove(id string) (Model, bool) {
	id = NormalizeID(id)
	m := c.Get(id)
	if m == nil {
		return Model{}, false
	}
	out := *m
	var next []Model
	for _, x := range c.Models {
		if x.ID != out.ID {
			next = append(next, x)
		}
	}
	c.Models = next
	if c.DefaultImage == out.ID {
		c.DefaultImage = ""
	}
	if c.DefaultVideo == out.ID {
		c.DefaultVideo = ""
	}
	c.reindex()
	return out, true
}

func (c *Catalog) reindex() {
	c.byID = map[string]*Model{}
	c.alias = map[string]string{}
	for i := range c.Models {
		m := &c.Models[i]
		c.byID[m.ID] = m
		c.alias[strings.ToLower(m.ID)] = m.ID
		for _, a := range m.Aliases {
			c.alias[strings.ToLower(a)] = m.ID
		}
	}
}

func (c *Catalog) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
