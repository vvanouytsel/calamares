package gh

import (
	"gopkg.in/yaml.v3"
)

// DispatchInput is one input of a workflow_dispatch trigger.
type DispatchInput struct {
	Name        string
	Description string
	Type        string // string, choice, boolean, number, environment
	Default     string
	Required    bool
	Options     []string
}

// ParseDispatchInputs reads a workflow file and reports whether it can be
// started manually, and with which inputs (in file order).
func ParseDispatchInputs(src string) (bool, []DispatchInput, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return false, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, nil, nil
	}
	on := mapGet(doc.Content[0], "on")
	if on == nil {
		on = mapGet(doc.Content[0], "true") // YAML 1.1 parsers turn `on` into a boolean
	}
	if on == nil {
		return false, nil, nil
	}
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == "workflow_dispatch", nil, nil
	case yaml.SequenceNode:
		for _, n := range on.Content {
			if n.Value == "workflow_dispatch" {
				return true, nil, nil
			}
		}
		return false, nil, nil
	case yaml.MappingNode:
	default:
		return false, nil, nil
	}
	wd, found := mapLookup(on, "workflow_dispatch")
	if !found {
		return false, nil, nil
	}
	if wd == nil || wd.Kind != yaml.MappingNode {
		return true, nil, nil
	}
	inputs := mapGet(wd, "inputs")
	if inputs == nil || inputs.Kind != yaml.MappingNode {
		return true, nil, nil
	}
	var out []DispatchInput
	for i := 0; i+1 < len(inputs.Content); i += 2 {
		in := DispatchInput{Name: inputs.Content[i].Value, Type: "string"}
		spec := inputs.Content[i+1]
		if spec.Kind == yaml.MappingNode {
			if n := mapGet(spec, "description"); n != nil {
				in.Description = n.Value
			}
			if n := mapGet(spec, "type"); n != nil && n.Value != "" {
				in.Type = n.Value
			}
			if n := mapGet(spec, "default"); n != nil {
				in.Default = n.Value
			}
			if n := mapGet(spec, "required"); n != nil {
				in.Required = n.Value == "true"
			}
			if n := mapGet(spec, "options"); n != nil && n.Kind == yaml.SequenceNode {
				for _, o := range n.Content {
					in.Options = append(in.Options, o.Value)
				}
			}
		}
		out = append(out, in)
	}
	return true, out, nil
}

func mapLookup(m *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Tag == "!!null" {
				return nil, true
			}
			return v, true
		}
	}
	return nil, false
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	v, _ := mapLookup(m, key)
	return v
}
