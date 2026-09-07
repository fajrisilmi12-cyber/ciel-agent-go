package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/approval"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/cron"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/memory"
	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/secrets"
)

// ---------- approvalTool ----------

type approvalTool struct {
	store *approval.Store
}

func (t *approvalTool) Name() string        { return "approval" }
func (t *approvalTool) Description() string { return "Manage exec approval bindings: list, approve, revoke, check a command hash." }
func (t *approvalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":   map[string]any{"type": "string", "enum": []string{"list", "approve", "revoke", "check"}},
			"command":  map[string]any{"type": "string", "description": "Command to approve or check."},
			"cwd":      map[string]any{"type": "string", "description": "Working directory for the command."},
			"id":       map[string]any{"type": "string", "description": "Approval ID (for revoke)."},
		},
		"required": []string{"action"},
	}
}

func (t *approvalTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Action  string `json:"action"`
		Command string `json:"command"`
		CWD     string `json:"cwd"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid approval arguments: %w", err)
	}
	switch input.Action {
	case "list":
		items, err := t.store.List(ctx, 20)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, a := range items {
			lines = append(lines, fmt.Sprintf("[%s] %s (cwd=%s, hits=%d, by=%s)", a.ID, a.CommandPreview, a.CWD, a.HitCount, a.ApprovedBy))
		}
		if len(lines) == 0 {
			return "no approvals", nil
		}
		return strings.Join(lines, "\n"), nil

	case "check":
		if strings.TrimSpace(input.Command) == "" {
			return "", fmt.Errorf("command is required for check")
		}
		cwd := input.CWD
		if cwd == "" {
			cwd = "."
		}
		ok, a, err := t.store.IsApproved(ctx, input.Command, cwd)
		if err != nil {
			return "", err
		}
		if !ok {
			hash := t.store.ComputeHash(input.Command, cwd)
			return fmt.Sprintf("NOT APPROVED\nhash: %s\ncommand: %s\ncwd: %s", hash, input.Command, cwd), nil
		}
		return fmt.Sprintf("APPROVED [id=%s hits=%d by=%s expires=%v]", a.ID, a.HitCount, a.ApprovedBy, a.ExpiresAt), nil

	case "approve":
		if strings.TrimSpace(input.Command) == "" {
			return "", fmt.Errorf("command is required for approve")
		}
		cwd := input.CWD
		if cwd == "" {
			cwd = "."
		}
		operands := approval.ExtractFileOperands(input.Command)
		a, err := t.store.Approve(ctx, input.Command, cwd, "agent", operands)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("APPROVED [id=%s hash=%s operands=%v]", a.ID, a.CommandHash, operands), nil

	case "revoke":
		if strings.TrimSpace(input.ID) == "" {
			return "", fmt.Errorf("id is required for revoke")
		}
		if err := t.store.Revoke(ctx, input.ID); err != nil {
			return "", err
		}
		return "revoked", nil

	default:
		return "", fmt.Errorf("unknown approval action %q", input.Action)
	}
}

// ---------- secretTool ----------

type secretTool struct {
	store *secrets.Store
}

func (t *secretTool) Name() string        { return "secret" }
func (t *secretTool) Description() string { return "Manage secrets: add, remove, list, resolve a SecretRef. Never output raw secret values." }
func (t *secretTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":  map[string]any{"type": "string", "enum": []string{"list", "add", "remove", "resolve"}},
			"key":     map[string]any{"type": "string"},
			"kind":    map[string]any{"type": "string", "enum": []string{"env", "file", "exec", "store"}},
			"source":  map[string]any{"type": "string", "description": "Value source for add (e.g. API_KEY, /path/file, command)."},
			"desc":    map[string]any{"type": "string"},
			"ref":     map[string]any{"type": "string", "description": "SecretRef to resolve (kind:value)."},
		},
		"required": []string{"action"},
	}
}

func (t *secretTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Desc   string `json:"desc"`
		Ref    string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid secret arguments: %w", err)
	}
	switch input.Action {
	case "list":
		items, err := t.store.List(ctx)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, s := range items {
			lines = append(lines, fmt.Sprintf("[%s] kind=%s source=%s desc=%s", s.Key, s.Kind, s.ValueSource, s.Description))
		}
		if len(lines) == 0 {
			return "no secrets registered", nil
		}
		return strings.Join(lines, "\n"), nil

	case "add":
		if input.Key == "" || input.Source == "" {
			return "", fmt.Errorf("key and source are required for add")
		}
		kind := secrets.SecretKind(input.Kind)
		if kind == "" {
			kind = secrets.KindEnv
		}
		if err := t.store.Add(ctx, input.Key, kind, input.Source, input.Desc); err != nil {
			return "", err
		}
		return fmt.Sprintf("secret %q registered (kind=%s)", input.Key, kind), nil

	case "remove":
		if input.Key == "" {
			return "", fmt.Errorf("key is required for remove")
		}
		if err := t.store.Remove(ctx, input.Key); err != nil {
			return "", err
		}
		return fmt.Sprintf("secret %q removed", input.Key), nil

	case "resolve":
		ref := input.Ref
		if ref == "" && input.Key != "" {
			ref = input.Key
		}
		if ref == "" {
			return "", fmt.Errorf("ref or key is required for resolve")
		}
		val, err := t.store.Resolve(ctx, ref)
		if err != nil {
			return "", err
		}
		// Never return raw value to agent — return length + masked preview
		masked := ""
		if len(val) > 4 {
			masked = val[:2] + "****" + val[len(val)-2:]
		} else {
			masked = "****"
		}
		return fmt.Sprintf("resolved (len=%d preview=%s)", len(val), masked), nil

	default:
		return "", fmt.Errorf("unknown secret action %q", input.Action)
	}
}

// ---------- cronTool ----------

type cronTool struct {
	scheduler *cron.Scheduler
}

func (t *cronTool) Name() string        { return "cron" }
func (t *cronTool) Description() string { return "Manage cron jobs: add, remove, list, enable, disable scheduled tasks." }
func (t *cronTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":      map[string]any{"type": "string", "enum": []string{"list", "add", "remove", "enable", "disable"}},
			"id":          map[string]any{"type": "string"},
			"name":        map[string]any{"type": "string"},
			"schedule":    map[string]any{"type": "string", "description": "5-field cron (min hour dom mon dow)."},
			"prompt":      map[string]any{"type": "string"},
			"tool_policy": map[string]any{"type": "string", "description": "* or comma-separated tool names."},
		},
		"required": []string{"action"},
	}
}

func (t *cronTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Action     string `json:"action"`
		ID         string `json:"id"`
		Name       string `json:"name"`
		Schedule   string `json:"schedule"`
		Prompt     string `json:"prompt"`
		ToolPolicy string `json:"tool_policy"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid cron arguments: %w", err)
	}
	switch input.Action {
	case "list":
		jobs, err := t.scheduler.ListJobs(ctx)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, j := range jobs {
			status := "enabled"
			if !j.Enabled {
				status = "disabled"
			}
			last := "-"
			if j.LastRun != nil {
				last = j.LastRun.Format(time.RFC3339)
			}
			lines = append(lines, fmt.Sprintf("[%s] %s | %s | %s | next=%s last=%s", j.ID, j.Name, j.Schedule, status, j.NextRun.Format(time.RFC3339), last))
		}
		if len(lines) == 0 {
			return "no cron jobs", nil
		}
		return strings.Join(lines, "\n"), nil

	case "add":
		if input.ID == "" || input.Schedule == "" || input.Prompt == "" {
			return "", fmt.Errorf("id, schedule, and prompt are required for add")
		}
		if input.Name == "" {
			input.Name = input.ID
		}
		policy := input.ToolPolicy
		if policy == "" {
			policy = "*"
		}
		if err := t.scheduler.AddJob(ctx, input.ID, input.Name, input.Schedule, input.Prompt, policy); err != nil {
			return "", err
		}
		return fmt.Sprintf("cron job %q added (schedule=%s)", input.ID, input.Schedule), nil

	case "remove":
		if input.ID == "" {
			return "", fmt.Errorf("id is required for remove")
		}
		if err := t.scheduler.RemoveJob(ctx, input.ID); err != nil {
			return "", err
		}
		return fmt.Sprintf("cron job %q removed", input.ID), nil

	case "enable":
		if input.ID == "" {
			return "", fmt.Errorf("id is required")
		}
		if err := t.scheduler.EnableJob(ctx, input.ID, true); err != nil {
			return "", err
		}
		return fmt.Sprintf("cron job %q enabled", input.ID), nil

	case "disable":
		if input.ID == "" {
			return "", fmt.Errorf("id is required")
		}
		if err := t.scheduler.EnableJob(ctx, input.ID, false); err != nil {
			return "", err
		}
		return fmt.Sprintf("cron job %q disabled", input.ID), nil

	default:
		return "", fmt.Errorf("unknown cron action %q", input.Action)
	}
}

// ---------- provenanceTool ----------

type provenanceTool struct {
	store *memory.ProvenanceStore
}

func (t *provenanceTool) Name() string        { return "provenance" }
func (t *provenanceTool) Description() string { return "Check memory provenance: origin class, taint status, and history for a memory target." }
func (t *provenanceTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"origin", "taint", "history"}},
			"target": map[string]any{"type": "string", "enum": []string{"user", "memory"}},
			"limit":  map[string]any{"type": "integer"},
		},
		"required": []string{"action", "target"},
	}
}

func (t *provenanceTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Action string `json:"action"`
		Target string `json:"target"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid provenance arguments: %w", err)
	}
	switch input.Action {
	case "origin":
		origin, err := t.store.GetOrigin(ctx, input.Target)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("origin: %s", origin), nil

	case "taint":
		tainted, err := t.store.IsTainted(ctx, input.Target)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("tainted: %v", tainted), nil

	case "history":
		if input.Limit <= 0 {
			input.Limit = 10
		}
		records, err := t.store.History(ctx, input.Target, input.Limit)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, r := range records {
			lines = append(lines, fmt.Sprintf("#%d origin=%s taint=%s hash=%s at=%s",
				r.ID, r.OriginClass, r.TaintFlags, r.ContentHash[:12], r.CreatedAt.Format(time.RFC3339)))
		}
		if len(lines) == 0 {
			return "no provenance records", nil
		}
		return strings.Join(lines, "\n"), nil

	default:
		return "", fmt.Errorf("unknown provenance action %q", input.Action)
	}
}

// ---------- Registration ----------

// RegisterAdvancedTools registers the advanced pattern tools.
func RegisterAdvancedTools(registry *Registry, db *sql.DB) error {
	approvalStore := approval.NewStore(db, 24)

	for _, tool := range []Tool{
		&approvalTool{store: approvalStore},
		&secretTool{store: secrets.NewStore(db)},
		&provenanceTool{store: memory.NewProvenanceStore(db)},
	} {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}

// RegisterCronTool registers the cron tool with its scheduler.
func RegisterCronTool(registry *Registry, scheduler *cron.Scheduler) error {
	return registry.Register(&cronTool{scheduler: scheduler})
}

// GetApprovalStore creates an ApprovalStore for use by the gated terminal tool.
func GetApprovalStore(db *sql.DB) *approval.Store {
	return approval.NewStore(db, 24)
}
