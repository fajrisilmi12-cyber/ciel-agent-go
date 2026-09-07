package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(context.Context, json.RawMessage) (string, error)
}

type Registry struct {
	items map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Tool)}
}

func (r *Registry) Register(tool Tool) error {
	if tool == nil || tool.Name() == "" {
		return fmt.Errorf("tool must have a name")
	}
	if _, exists := r.items[tool.Name()]; exists {
		return fmt.Errorf("tool %q is already registered", tool.Name())
	}
	r.items[tool.Name()] = tool
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.items[name]
	return tool, ok
}

func (r *Registry) Tools() []Tool {
	items := make([]Tool, 0, len(r.items))
	for _, tool := range r.items {
		items = append(items, tool)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
	return items
}
