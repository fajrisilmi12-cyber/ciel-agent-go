package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/memory"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/providers"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/tools"
)

// ToolHook is called before or after each tool execution.
// PreHook: return non-nil error to block execution.
// PostHook: mutate *result to filter/redact output.
type PreHook func(ctx context.Context, sessionID, toolName string, args json.RawMessage) error
type PostHook func(ctx context.Context, sessionID, toolName string, args json.RawMessage, result *string)

type Runner struct {
	db            *sql.DB
	provider      providers.Provider
	model         string
	maxIterations int
	systemPrompt  string
	registry      *tools.Registry
	allowedTools  map[string]bool
	historyLimit  int

	// OpenClaw-inspired hooks
	recoveryStore *state.RecoveryStore
	provStore     *memory.ProvenanceStore
	preHooks      []PreHook
	postHooks     []PostHook
}

func NewRunner(db *sql.DB, provider providers.Provider, model string, maxIterations int, registry *tools.Registry) *Runner {
	if maxIterations <= 0 {
		maxIterations = 20
	}
	return &Runner{db: db, provider: provider, model: model, maxIterations: maxIterations, historyLimit: 50, systemPrompt: "You are Ciel Agent, a helpful autonomous AI assistant.", registry: registry}
}

func (r *Runner) SetRecoveryStore(store *state.RecoveryStore) { r.recoveryStore = store }
func (r *Runner) SetProvenanceStore(store *memory.ProvenanceStore) { r.provStore = store }
func (r *Runner) AddPreHook(h PreHook)  { r.preHooks = append(r.preHooks, h) }
func (r *Runner) AddPostHook(h PostHook) { r.postHooks = append(r.postHooks, h) }

func (r *Runner) SetAllowedTools(names []string) {
	r.allowedTools = make(map[string]bool, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			r.allowedTools[strings.TrimSpace(name)] = true
		}
	}
}

func (r *Runner) SetHistoryLimit(limit int) {
	if limit > 0 {
		r.historyLimit = limit
	}
}

func (r *Runner) toolAllowed(name string) bool {
	return len(r.allowedTools) == 0 || r.allowedTools[name]
}

func (r *Runner) SetSystemPrompt(prompt string) {
	if strings.TrimSpace(prompt) != "" {
		r.systemPrompt = prompt
	}
}

func (r *Runner) Run(ctx context.Context, sessionID, userInput string) (string, string, error) {
	if strings.TrimSpace(userInput) == "" {
		return sessionID, "", fmt.Errorf("message cannot be empty")
	}
	if sessionID == "" {
		var err error
		sessionID, err = state.CreateSession(ctx, r.db, "default", r.model)
		if err != nil {
			return sessionID, "", err
		}
	}
	if err := state.AddMessage(ctx, r.db, sessionID, providers.Message{Role: "user", Content: userInput}); err != nil {
		return sessionID, "", err
	}
	history, err := state.GetMessages(ctx, r.db, sessionID, r.historyLimit)
	if err != nil {
		return sessionID, "", err
	}
	systemPrompt := AddToolCatalog(r.systemPrompt, r.registry)
	messages := append([]providers.Message{{Role: "system", Content: systemPrompt}}, history...)
	toolCallCounts := make(map[string]int)
	lastToolName := ""

	// Taint tracking: web_fetch / web_search inject untrusted content
	tainted := false

	for iteration := 0; iteration < r.maxIterations; iteration++ {
		// --- Session Recovery: save checkpoint at each iteration ---
		if r.recoveryStore != nil {
			_ = r.recoveryStore.SaveCheckpointMessages(ctx, sessionID, iteration, messages, map[string]any{
				"tainted": tainted,
			})
		}

		request := providers.CompletionRequest{Model: r.model, Messages: messages}
		if r.registry != nil {
			for _, tool := range r.registry.Tools() {
				if !r.toolAllowed(tool.Name()) {
					continue
				}
				request.Tools = append(request.Tools, providers.ToolDefinition{
					Type: "function",
					Function: providers.FunctionSpec{
						Name:        tool.Name(),
						Description: tool.Description(),
						Parameters:  tool.Parameters(),
					},
				})
			}
		}
		response, err := r.provider.Complete(ctx, request)
		if err != nil {
			return sessionID, "", err
		}
		if err := state.AddMessage(ctx, r.db, sessionID, response.Message); err != nil {
			return sessionID, "", err
		}
		messages = append(messages, response.Message)
		if len(response.Message.ToolCalls) > 0 {
			if r.registry == nil {
				return sessionID, "", fmt.Errorf("provider requested tools but no tool registry is configured")
			}
			for _, call := range response.Message.ToolCalls {
				if !r.toolAllowed(call.Function.Name) {
					return sessionID, "", fmt.Errorf("tool %q is not allowed for this session", call.Function.Name)
				}
				fingerprint := call.Function.Name + "\x00" + call.Function.Arguments
				toolCallCounts[fingerprint]++
				lastToolName = call.Function.Name
				if toolCallCounts[fingerprint] > 3 {
					return sessionID, "", fmt.Errorf("agent repeated tool %q more than 3 times; stopping loop", call.Function.Name)
				}
				tool, ok := r.registry.Get(call.Function.Name)
				if !ok {
					return sessionID, "", fmt.Errorf("unknown tool %q", call.Function.Name)
				}

				// --- Pre-hooks (e.g. approval gating lives inside terminalTool) ---
				args := json.RawMessage(call.Function.Arguments)
				blocked := false
				for _, h := range r.preHooks {
					if err := h(ctx, sessionID, call.Function.Name, args); err != nil {
						result := fmt.Sprintf("hook blocked: %v", err)
						toolMessage := providers.Message{Role: "tool", Content: result, ToolCallID: call.ID, Name: call.Function.Name}
						_ = state.AddMessage(ctx, r.db, sessionID, toolMessage)
						messages = append(messages, toolMessage)
						blocked = true
						break
					}
				}
				if blocked {
					continue
				}

				result, err := tool.Execute(ctx, args)
				if err != nil {
					result = fmt.Sprintf("tool error: %v", err)
				}

				// --- Taint propagation ---
				if call.Function.Name == "web_fetch" || call.Function.Name == "web_search" {
					tainted = true
				}

				// --- Post-hooks (egress redaction, provenance logging) ---
				for _, h := range r.postHooks {
					h(ctx, sessionID, call.Function.Name, args, &result)
				}

				toolMessage := providers.Message{Role: "tool", Content: result, ToolCallID: call.ID, Name: call.Function.Name}
				if err := state.AddMessage(ctx, r.db, sessionID, toolMessage); err != nil {
					return sessionID, "", err
				}
				messages = append(messages, toolMessage)
			}
			continue
		}
		// --- Clean up recovery checkpoints on success ---
		if r.recoveryStore != nil {
			if turns, err := r.recoveryStore.FindInterrupted(ctx, sessionID); err == nil {
				for _, t := range turns {
					_ = r.recoveryStore.MarkRecovered(ctx, t.ID)
				}
			}
		}
		return sessionID, response.Message.Content, nil
	}
	if lastToolName != "" {
		return sessionID, "", fmt.Errorf("agent exceeded maximum iterations while calling tool %q", lastToolName)
	}
	return sessionID, "", fmt.Errorf("agent exceeded maximum iterations")
}
