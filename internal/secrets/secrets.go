package secrets

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SecretKind describes how to resolve a secret value.
type SecretKind string

const (
	KindEnv   SecretKind = "env"   // os.Getenv
	KindFile  SecretKind = "file"  // read from file
	KindExec  SecretKind = "exec"  // run command, capture stdout
	KindStore SecretKind = "store" // raw value stored in DB
)

// Secret is a registered secret reference (no plaintext value).
type Secret struct {
	ID          int64
	Key         string
	Kind        SecretKind
	ValueSource string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Store manages SecretRef entries and resolves them.
type Store struct {
	db *sql.DB
}

// NewStore returns a ready-to-use Store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Add registers (or updates) a secret reference.
func (s *Store) Add(ctx context.Context, key string, kind SecretKind, valueSource, description string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO secrets (key, kind, value_source, description, updated_at) VALUES (?,?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET kind=excluded.kind, value_source=excluded.value_source,
		 description=excluded.description, updated_at=excluded.updated_at`,
		key, string(kind), valueSource, description, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("add secret: %w", err)
	}
	return nil
}

// Remove deletes a secret reference.
func (s *Store) Remove(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM secrets WHERE key=?`, key)
	return err
}

// List returns all registered secret references (without resolving).
func (s *Store) List(ctx context.Context) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key, kind, value_source, description, created_at, updated_at FROM secrets ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		var sec Secret
		var ca, ua string
		if err := rows.Scan(&sec.ID, &sec.Key, &sec.Kind, &sec.ValueSource, &sec.Description, &ca, &ua); err != nil {
			return nil, err
		}
		sec.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		sec.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, sec)
	}
	return out, rows.Err()
}

// Resolve follows a "kind:value" ref string.
func (s *Store) Resolve(ctx context.Context, ref string) (string, error) {
	kind, val := parseRef(ref)
	switch kind {
	case KindEnv:
		return resolveEnv(val)
	case KindFile:
		return resolveFile(val)
	case KindExec:
		return resolveExec(ctx, val)
	case KindStore:
		return s.resolveStore(ctx, val)
	default:
		return "", fmt.Errorf("unknown secret kind %q", kind)
	}
}

// ResolveKey looks up a secret by its registered key, then resolves it.
func (s *Store) ResolveKey(ctx context.Context, key string) (string, error) {
	var kind, valueSource string
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, value_source FROM secrets WHERE key=?`, key).
		Scan(&kind, &valueSource)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("secret %q not found", key)
	}
	if err != nil {
		return "", fmt.Errorf("resolve key: %w", err)
	}
	return s.Resolve(ctx, kind+":"+valueSource)
}

// RedactSensitive scans output for known secret values and replaces them.
func (s *Store) RedactSensitive(ctx context.Context, output string) (string, bool) {
	secrets, err := s.List(ctx)
	if err != nil {
		return output, false
	}
	redacted := false
	for _, sec := range secrets {
		val, err := s.Resolve(ctx, string(sec.Kind)+":"+sec.ValueSource)
		if err != nil || len(val) < 4 {
			continue
		}
		if strings.Contains(output, val) {
			output = strings.ReplaceAll(output, val, "[REDACTED:"+sec.Key+"]")
			redacted = true
		}
	}
	return output, redacted
}

// ---------- helpers ----------

func parseRef(ref string) (SecretKind, string) {
	idx := strings.Index(ref, ":")
	if idx < 0 {
		return KindEnv, ref
	}
	return SecretKind(ref[:idx]), ref[idx+1:]
}

func resolveEnv(name string) (string, error) {
	v := os.Getenv(strings.TrimSpace(name))
	if v == "" {
		return "", fmt.Errorf("env %q is empty or unset", name)
	}
	return v, nil
}

func resolveFile(path string) (string, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func resolveExec(ctx context.Context, command string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("exec secret command: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *Store) resolveStore(ctx context.Context, key string) (string, error) {
	var val string
	err := s.db.QueryRowContext(ctx,
		`SELECT value_source FROM secrets WHERE key=?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("stored secret %q not found", key)
	}
	return val, err
}
