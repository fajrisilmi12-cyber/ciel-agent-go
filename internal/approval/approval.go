package approval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Approval represents a single approved command binding.
type Approval struct {
	ID             string
	CommandHash    string
	CommandPreview string
	CWD            string
	EnvHash        string
	FileOperands   []string
	ApprovedBy     string
	ApprovedAt     time.Time
	ExpiresAt      *time.Time
	HitCount       int
}

// Store manages exec-approval bindings in SQLite.
type Store struct {
	db            *sql.DB
	defaultExpiry time.Duration
}

// NewStore returns a Store that expires approvals after `expiryHours`.
func NewStore(db *sql.DB, expiryHours int) *Store {
	if expiryHours <= 0 {
		expiryHours = 24
	}
	return &Store{db: db, defaultExpiry: time.Duration(expiryHours) * time.Hour}
}

// ComputeHash produces a deterministic hash from command + cwd + env.
func (s *Store) ComputeHash(command, cwd string) string {
	command = strings.TrimSpace(command)
	cwd = strings.TrimSpace(cwd)
	envHash := EnvHash()
	combined := command + "\x00" + cwd + "\x00" + envHash
	h := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(h[:])
}

// EnvHash returns a summary hash of PATH-related env vars.
func EnvHash() string {
	keys := []string{"PATH", "HOME", "USER", "SHELL", "COMSPEC", "SYSTEMROOT", "WINDIR"}
	var parts []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// IsApproved checks whether a command+cwd pair is currently approved.
func (s *Store) IsApproved(ctx context.Context, command, cwd string) (bool, *Approval, error) {
	hash := s.ComputeHash(command, cwd)
	var a Approval
	var expiresAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, command_hash, command_preview, cwd, env_hash, file_operands, approved_by, approved_at, expires_at, hit_count
		 FROM exec_approvals WHERE command_hash = ?`, hash).
		Scan(&a.ID, &a.CommandHash, &a.CommandPreview, &a.CWD, &a.EnvHash,
			&a.FileOperands, &a.ApprovedBy, &a.ApprovedAt, &expiresAt, &a.HitCount)
	if err == sql.ErrNoRows {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("check approval: %w", err)
	}
	// Unmarshal file_operands JSON
	if raw := strings.TrimSpace(fmt.Sprint(a.FileOperands)); raw != "" && raw != "[]" {
		_ = json.Unmarshal([]byte(raw), &a.FileOperands)
	}
	// Check expiry
	if expiresAt.Valid && expiresAt.String != "" {
		if exp, e := time.Parse(time.RFC3339, expiresAt.String); e == nil && time.Now().After(exp) {
			return false, nil, nil
		}
	}
	return true, &a, nil
}

// Approve creates or updates an approval binding.
func (s *Store) Approve(ctx context.Context, command, cwd, approvedBy string, fileOperands []string) (*Approval, error) {
	hash := s.ComputeHash(command, cwd)
	id := hash[:16]
	if fileOperands == nil {
		fileOperands = []string{}
	}
	operandsJSON, _ := json.Marshal(fileOperands)
	expires := time.Now().Add(s.defaultExpiry)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO exec_approvals (id, command_hash, command_preview, cwd, env_hash, file_operands, approved_by, expires_at, hit_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(command_hash) DO UPDATE SET
			approved_by = excluded.approved_by, approved_at = CURRENT_TIMESTAMP,
			expires_at = excluded.expires_at, hit_count = 0`,
		id, hash, truncate(command, 500), cwd, EnvHash(), string(operandsJSON),
		approvedBy, expires.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("approve command: %w", err)
	}
	return s.GetByID(ctx, id)
}

// Revoke removes an approval by id.
func (s *Store) Revoke(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM exec_approvals WHERE id = ?`, id)
	return err
}

// GetByID fetches a single approval.
func (s *Store) GetByID(ctx context.Context, id string) (*Approval, error) {
	var a Approval
	var expiresAt sql.NullString
	var operandsRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, command_hash, command_preview, cwd, env_hash, file_operands, approved_by, approved_at, expires_at, hit_count
		 FROM exec_approvals WHERE id = ?`, id).
		Scan(&a.ID, &a.CommandHash, &a.CommandPreview, &a.CWD, &a.EnvHash,
			&operandsRaw, &a.ApprovedBy, &a.ApprovedAt, &expiresAt, &a.HitCount)
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		exp, _ := time.Parse(time.RFC3339, expiresAt.String)
		a.ExpiresAt = &exp
	}
	if operandsRaw != "" {
		_ = json.Unmarshal([]byte(operandsRaw), &a.FileOperands)
	}
	return &a, nil
}

// IncrementHit records one more successful use of an approval.
func (s *Store) IncrementHit(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE exec_approvals SET hit_count = hit_count + 1 WHERE id = ?`, id)
	return err
}

// List returns the most recent approvals.
func (s *Store) List(ctx context.Context, limit int) ([]Approval, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, command_hash, command_preview, cwd, env_hash, file_operands, approved_by, approved_at, expires_at, hit_count
		 FROM exec_approvals ORDER BY approved_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Approval
	for rows.Next() {
		var a Approval
		var expiresAt sql.NullString
		var operandsRaw string
		if err := rows.Scan(&a.ID, &a.CommandHash, &a.CommandPreview, &a.CWD, &a.EnvHash,
			&operandsRaw, &a.ApprovedBy, &a.ApprovedAt, &expiresAt, &a.HitCount); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			exp, _ := time.Parse(time.RFC3339, expiresAt.String)
			a.ExpiresAt = &exp
		}
		if operandsRaw != "" {
			_ = json.Unmarshal([]byte(operandsRaw), &a.FileOperands)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevokeExpired deletes all expired approvals; returns count removed.
func (s *Store) RevokeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM exec_approvals WHERE expires_at IS NOT NULL AND expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ExtractFileOperands best-effort extracts paths from a shell command.
var pathRe = regexp.MustCompile(`(?:/[\w.\-]+(?:/[\w.\-]+)*|(?:\./|\.\./)[\w.\-]+(?:/[\w.\-]+)*)`)

func ExtractFileOperands(command string) []string {
	matches := pathRe.FindAllString(command, -1)
	seen := make(map[string]bool)
	var out []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
