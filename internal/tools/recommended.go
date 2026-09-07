package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type todoTool struct {
	mu    sync.Mutex
	items []map[string]any
}

func (tool *todoTool) Name() string { return "todo" }
func (tool *todoTool) Description() string {
	return "Create, list, update, or clear an in-memory task plan."
}
func (tool *todoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "replace", "clear"}},
			"items":  map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
		}, "required": []string{"action"},
	}
}
func (tool *todoTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Action string           `json:"action"`
		Items  []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid todo arguments: %w", err)
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	switch input.Action {
	case "replace":
		tool.items = input.Items
	case "clear":
		tool.items = nil
	case "list":
	default:
		return "", fmt.Errorf("unsupported todo action %q", input.Action)
	}
	data, _ := json.Marshal(tool.items)
	return string(data), nil
}

type processTool struct{}

func (processTool) Name() string { return "process" }
func (processTool) Description() string {
	return "List running processes or inspect a process by name."
}
func (processTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}
}
func (processTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid process arguments: %w", err)
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		args := []string{"/FO", "TABLE", "/NH"}
		if strings.TrimSpace(input.Name) != "" {
			args = append(args, "/FI", "IMAGENAME eq "+input.Name)
		}
		command = exec.CommandContext(commandContext, "tasklist", args...)
	} else {
		command = exec.CommandContext(commandContext, "ps", "-eo", "pid,comm,etime")
	}
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("list processes: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

type sessionSearchTool struct{ db *sql.DB }

func (tool sessionSearchTool) Name() string { return "session_search" }
func (tool sessionSearchTool) Description() string {
	return "Search previous conversation messages by text."
}
func (tool sessionSearchTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}, "required": []string{"query"}}
}
func (tool sessionSearchTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid session_search arguments: %w", err)
	}
	if strings.TrimSpace(input.Query) == "" {
		return "", fmt.Errorf("search query cannot be empty")
	}
	if input.Limit <= 0 || input.Limit > 20 {
		input.Limit = 10
	}
	rows, err := tool.db.QueryContext(ctx, `SELECT session_id, role, content FROM messages WHERE content LIKE ? ORDER BY id DESC LIMIT ?`, "%"+input.Query+"%", input.Limit)
	if err != nil {
		return "", fmt.Errorf("search sessions: %w", err)
	}
	defer rows.Close()
	var results []string
	for rows.Next() {
		var sessionID, role, content string
		if err := rows.Scan(&sessionID, &role, &content); err != nil {
			return "", err
		}
		results = append(results, fmt.Sprintf("[%s] %s: %s", sessionID, role, content))
	}
	if len(results) == 0 {
		return "no matches", nil
	}
	return strings.Join(results, "\n"), nil
}

type clarifyTool struct{}

func (clarifyTool) Name() string { return "clarify" }
func (clarifyTool) Description() string {
	return "Ask the user a clarification question when a task is ambiguous."
}
func (clarifyTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"question": map[string]any{"type": "string"}, "options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"question"}}
}
func (clarifyTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || strings.TrimSpace(input.Question) == "" {
		return "", fmt.Errorf("clarify requires a question")
	}
	data, _ := json.Marshal(map[string]any{"clarification_required": true, "question": input.Question, "options": input.Options})
	return string(data), nil
}

type webSearchTool struct{}

func (webSearchTool) Name() string        { return "web_search" }
func (webSearchTool) Description() string { return "Search the public web using DuckDuckGo." }
func (webSearchTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}
}
func (webSearchTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || strings.TrimSpace(input.Query) == "" {
		return "", fmt.Errorf("web_search requires a query")
	}
	requestContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(input.Query)
	request, _ := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
	request.Header.Set("User-Agent", "Hermes-Agent-Go/0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("web search: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("web search returned HTTP %s", response.Status)
	}
	return string(body), nil
}

func RegisterRecommended(registry *Registry) error {
	for _, tool := range []Tool{&todoTool{}, processTool{}, clarifyTool{}, webSearchTool{}} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

func RegisterSessionSearch(registry *Registry, db *sql.DB) error {
	return registry.Register(sessionSearchTool{db: db})
}
