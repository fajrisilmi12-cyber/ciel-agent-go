package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/approval"
	"golang.org/x/net/html"
)

func RegisterBuiltins(registry *Registry, root string) error {
	for _, tool := range []Tool{
		fileTool{root: root},
		searchFilesTool{root: root},
		&terminalTool{},
		webFetchTool{},
		NewZernioPosterFromEnv(),
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	if err := RegisterRecommended(registry); err != nil {
		return err
	}
	return nil
}

type webFetchTool struct{}

func (webFetchTool) Name() string { return "web_fetch" }

func (webFetchTool) Description() string {
	return "Fetch a public HTTP or HTTPS URL and return its readable text."
}

func (webFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string"},
		},
		"required": []string{"url"},
	}
}

func (webFetchTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid web_fetch arguments: %w", err)
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("url must be a valid http or https URL")
	}
	requestContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create web request: %w", err)
	}
	request.Header.Set("User-Agent", "Ciel-Agent-Go/0.1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch URL: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("fetch URL returned HTTP %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 5<<20))
	if err != nil {
		return "", fmt.Errorf("read URL response: %w", err)
	}
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "html") {
		return extractHTMLText(bytes.NewReader(body))
	}
	return string(body), nil
}

func extractHTMLText(reader io.Reader) (string, error) {
	document, err := html.Parse(reader)
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}
	var builder strings.Builder
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		if node.Type == html.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				builder.WriteString(text)
				builder.WriteByte(' ')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return strings.TrimSpace(builder.String()), nil
}

type searchFilesTool struct {
	root string
}

func (searchFilesTool) Name() string { return "search_files" }

func (searchFilesTool) Description() string {
	return "Search text in files inside the working directory."
}

func (searchFilesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"path":  map[string]any{"type": "string", "description": "Optional relative directory or file."},
		},
		"required": []string{"query"},
	}
}

func (tool searchFilesTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid search arguments: %w", err)
	}
	if strings.TrimSpace(input.Query) == "" {
		return "", fmt.Errorf("search query cannot be empty")
	}
	start := tool.root
	if strings.TrimSpace(input.Path) != "" {
		var err error
		start, err = (fileTool{root: tool.root}).safePath(input.Path)
		if err != nil {
			return "", err
		}
	}
	var matches []string
	err := filepath.Walk(start, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if info.IsDir() {
			if path != start && (info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 1<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), input.Query) {
			return nil
		}
		relative, err := filepath.Rel(tool.root, path)
		if err == nil {
			matches = append(matches, relative)
		}
		if len(matches) >= 50 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("search files: %w", err)
	}
	if len(matches) == 0 {
		return "no matches", nil
	}
	return strings.Join(matches, "\n"), nil
}

type fileTool struct {
	root string
}

func (fileTool) Name() string { return "file" }

func (fileTool) Description() string { return "Read or write a file inside the working directory." }

func (fileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"read", "write"}},
			"path":      map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
		},
		"required": []string{"operation", "path"},
	}
}

func (tool fileTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Operation string `json:"operation"`
		Path      string `json:"path"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid file arguments: %w", err)
	}
	path, err := tool.safePath(input.Path)
	if err != nil {
		return "", err
	}
	switch input.Operation {
	case "read":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		return string(data), nil
	case "write":
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("create parent directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(input.Content), 0o644); err != nil {
			return "", fmt.Errorf("write file: %w", err)
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(input.Content), input.Path), nil
	default:
		return "", fmt.Errorf("unsupported file operation %q", input.Operation)
	}
}

func (tool fileTool) safePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("file path cannot be empty")
	}
	root, err := filepath.Abs(tool.root)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(root, path))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file path escapes working directory")
	}
	return candidate, nil
}

type terminalTool struct {
	approvalStore *approval.Store // nil = no approval gating
	egressFilter  func(string) (string, bool)
}

func (*terminalTool) Name() string        { return "terminal" }
func (*terminalTool) Description() string { return "Run a shell command with a short timeout." }

func (*terminalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
		},
		"required": []string{"command"},
	}
}

func (tool *terminalTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid terminal arguments: %w", err)
	}
	if strings.TrimSpace(input.Command) == "" {
		return "", fmt.Errorf("terminal command cannot be empty")
	}

	// --- Exec Approval Binding ---
	if tool.approvalStore != nil {
		cwd, _ := os.Getwd()
		ok, appr, checkErr := tool.approvalStore.IsApproved(ctx, input.Command, cwd)
		if checkErr != nil {
			return "", fmt.Errorf("approval check: %w", checkErr)
		}
		if !ok {
			hash := tool.approvalStore.ComputeHash(input.Command, cwd)
			operands := approval.ExtractFileOperands(input.Command)
			return fmt.Sprintf(
				"COMMAND REQUIRES APPROVAL\nhash: %s\ncwd: %s\noperands: %v\ncommand: %s\n\nUse the approval tool with action=approve to approve this command.",
				hash, cwd, operands, input.Command), nil
		}
		_ = tool.approvalStore.IncrementHit(ctx, appr.ID)
	}

	commandContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		command = exec.CommandContext(commandContext, "cmd", "/C", input.Command)
	} else {
		command = exec.CommandContext(commandContext, "sh", "-c", input.Command)
	}
	output, err := command.CombinedOutput()
	result := strings.TrimSpace(string(output))

	// --- Egress sentinel: redact secrets from output ---
	if tool.egressFilter != nil && result != "" {
		if filtered, redacted := tool.egressFilter(result); redacted {
			result = filtered + "\n\n[EGRESS: sensitive values redacted]"
		}
	}

	if err != nil {
		return result, fmt.Errorf("command failed: %w: %s", err, result)
	}
	return result, nil
}

// ConfigureTerminalTool finds the registered terminal tool and sets
// approval gating and egress filtering on it.
func ConfigureTerminalTool(registry *Registry, approvalStore *approval.Store, egressFilter func(string) (string, bool)) {
	for _, t := range registry.Tools() {
		if tt, ok := t.(*terminalTool); ok {
			tt.approvalStore = approvalStore
			tt.egressFilter = egressFilter
			return
		}
	}
}
