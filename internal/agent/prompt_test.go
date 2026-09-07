package agent

import (
	"strings"
	"testing"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
)

func TestBuildSystemPromptIncludesBoundedContexts(t *testing.T) {
	prompt := BuildSystemPrompt("base", "calm and technical", "user prefers Go", "Skill: review")
	if !strings.Contains(prompt, "<persona>") || !strings.Contains(prompt, "calm and technical") || !strings.Contains(prompt, "<memory_context>") || !strings.Contains(prompt, "user prefers Go") || !strings.Contains(prompt, "<skill_context>") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestAddToolCatalogIncludesZernio(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ZernioPoster{}); err != nil {
		t.Fatal(err)
	}
	prompt := AddToolCatalog("base", registry)
	if !strings.Contains(prompt, "zernio_post") {
		t.Fatalf("prompt = %q", prompt)
	}
}
