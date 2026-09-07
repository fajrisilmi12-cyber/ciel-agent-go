package agent

import (
	"strings"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
)

func BuildSystemPrompt(base, persona, memoryContext, skillContext string) string {
	sections := []string{strings.TrimSpace(base)}
	if strings.TrimSpace(persona) != "" {
		sections = append(sections, "<persona>\n"+strings.TrimSpace(persona)+"\n</persona>")
	}
	if strings.TrimSpace(memoryContext) != "" {
		sections = append(sections, "<memory_context>\n"+strings.TrimSpace(memoryContext)+"\n</memory_context>")
	}
	if strings.TrimSpace(skillContext) != "" {
		sections = append(sections, "<skill_context>\n"+strings.TrimSpace(skillContext)+"\n</skill_context>")
	}
	return strings.Join(sections, "\n\n")
}

func AddToolCatalog(prompt string, registry *tools.Registry) string {
	if registry == nil {
		return prompt
	}
	var lines []string
	for _, tool := range registry.Tools() {
		lines = append(lines, "- "+tool.Name()+": "+tool.Description())
	}
	if len(lines) == 0 {
		return prompt
	}
	return strings.TrimSpace(prompt) + "\n\n<available_tools>\n" + strings.Join(lines, "\n") + "\n</available_tools>"
}
