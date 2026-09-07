package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/providers"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
)

type scriptedProvider struct {
	calls int
}

type repeatingProvider struct{}

type captureProvider struct {
	request providers.CompletionRequest
}

func (p *captureProvider) Complete(_ context.Context, request providers.CompletionRequest) (providers.CompletionResponse, error) {
	p.request = request
	return providers.CompletionResponse{Message: providers.Message{Role: "assistant", Content: "ok"}}, nil
}

func (repeatingProvider) Complete(_ context.Context, request providers.CompletionRequest) (providers.CompletionResponse, error) {
	var call providers.ToolCall
	call.ID = "repeat"
	call.Type = "function"
	call.Function.Name = "echo"
	call.Function.Arguments = `{"value":"same"}`
	return providers.CompletionResponse{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}}}, nil
}

func (p *scriptedProvider) Complete(_ context.Context, request providers.CompletionRequest) (providers.CompletionResponse, error) {
	p.calls++
	if p.calls == 1 {
		var call providers.ToolCall
		call.ID = "call-1"
		call.Type = "function"
		call.Function.Name = "echo"
		call.Function.Arguments = `{"value":"done"}`
		return providers.CompletionResponse{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}}}, nil
	}
	return providers.CompletionResponse{Message: providers.Message{Role: "assistant", Content: "finished"}}, nil
}

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "Echo a value." }
func (echoTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
}
func (echoTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", err
	}
	return input.Value, nil
}

func TestRunnerExecutesToolAndPersistsConversation(t *testing.T) {
	db, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := tools.NewRegistry()
	if err := registry.Register(echoTool{}); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{}
	runner := NewRunner(db, provider, "test-model", 3, registry)
	_, response, err := runner.Run(context.Background(), "", "run echo")
	if err != nil {
		t.Fatal(err)
	}
	if response != "finished" {
		t.Fatalf("response = %q, want finished", response)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("message count = %d, want 4", count)
	}
}

func TestRunnerStopsRepeatedToolCalls(t *testing.T) {
	db, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := tools.NewRegistry()
	if err := registry.Register(echoTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(db, repeatingProvider{}, "test-model", 20, registry)
	_, _, err = runner.Run(context.Background(), "", "repeat")
	if err == nil || !strings.Contains(err.Error(), "repeated tool") {
		t.Fatalf("error = %v, want repeated tool error", err)
	}
}

func TestRunnerFiltersDisallowedTools(t *testing.T) {
	db, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	registry := tools.NewRegistry()
	if err := tools.RegisterBuiltins(registry, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	provider := &captureProvider{}
	runner := NewRunner(db, provider, "test-model", 2, registry)
	runner.SetAllowedTools([]string{"file", "search_files"})
	if _, _, err := runner.Run(context.Background(), "", "hello"); err != nil {
		t.Fatal(err)
	}
	for _, definition := range provider.request.Tools {
		if definition.Function.Name == "terminal" || definition.Function.Name == "zernio_post" {
			t.Fatalf("disallowed tool exposed: %s", definition.Function.Name)
		}
	}
}
