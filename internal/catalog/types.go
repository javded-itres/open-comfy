package catalog

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
