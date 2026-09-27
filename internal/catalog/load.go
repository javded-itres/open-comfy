package catalog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

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
