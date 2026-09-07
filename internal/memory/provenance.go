package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// OriginClass describes who or what produced a piece of memory.
type OriginClass string

const (
	OriginOwner     OriginClass = "owner"     // human-provided
	OriginAgent     OriginClass = "agent"     // LLM-generated
	OriginUntrusted OriginClass = "untrusted" // from network / external tool
	OriginSystem    OriginClass = "system"    // config / bootstrap
)

// ProvenanceRecord is one provenance entry.
type ProvenanceRecord struct {
	ID          int64
	Target      string
	OriginClass OriginClass
	TaintFlags  string
	ContentHash string
	CreatedAt   time.Time
}

// ProvenanceStore tracks where memory content came from.
type ProvenanceStore struct {
	db *sql.DB
}

// NewProvenanceStore returns a ready-to-use ProvenanceStore.
func NewProvenanceStore(db *sql.DB) *ProvenanceStore {
	return &ProvenanceStore{db: db}
}

func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// Record writes a provenance entry.
func (s *ProvenanceStore) Record(ctx context.Context, target string, origin OriginClass, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_provenance (target, origin_class, content_hash, created_at) VALUES (?, ?, ?, ?)`,
		target, string(origin), contentHash(content), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record provenance: %w", err)
	}
	return nil
}

// RecordTainted writes a provenance entry with taint flags.
func (s *ProvenanceStore) RecordTainted(ctx context.Context, target string, origin OriginClass, content, taintFlags string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_provenance (target, origin_class, taint_flags, content_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		target, string(origin), taintFlags, contentHash(content), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record tainted provenance: %w", err)
	}
	return nil
}

// GetOrigin returns the most-recent origin class for a target.
func (s *ProvenanceStore) GetOrigin(ctx context.Context, target string) (OriginClass, error) {
	var origin string
	err := s.db.QueryRowContext(ctx,
		`SELECT origin_class FROM memory_provenance WHERE target = ? ORDER BY id DESC LIMIT 1`, target).
		Scan(&origin)
	if err == sql.ErrNoRows {
		return OriginSystem, nil
	}
	if err != nil {
		return "", fmt.Errorf("get origin: %w", err)
	}
	return OriginClass(origin), nil
}

// History returns the last `limit` provenance records for a target.
func (s *ProvenanceStore) History(ctx context.Context, target string, limit int) ([]ProvenanceRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target, origin_class, taint_flags, content_hash, created_at
		 FROM memory_provenance WHERE target = ? ORDER BY id DESC LIMIT ?`, target, limit)
	if err != nil {
		return nil, fmt.Errorf("provenance history: %w", err)
	}
	defer rows.Close()
	var records []ProvenanceRecord
	for rows.Next() {
		var r ProvenanceRecord
		var createdAt string
		if err := rows.Scan(&r.ID, &r.Target, &r.OriginClass, &r.TaintFlags, &r.ContentHash, &createdAt); err != nil {
			return nil, fmt.Errorf("scan provenance: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		records = append(records, r)
	}
	return records, rows.Err()
}

// IsTainted reports whether any tainted record exists for the target.
func (s *ProvenanceStore) IsTainted(ctx context.Context, target string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_provenance WHERE target = ? AND taint_flags != ''`, target).
		Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check taint: %w", err)
	}
	return count > 0, nil
}

// EnvHash returns a deterministic hash of the current PATH-related
// environment variables (used for approval drift detection).
func EnvHash() string {
	keys := []string{"PATH", "HOME", "USER", "SHELL", "COMSPEC", "SYSTEMROOT", "WINDIR"}
	var parts []string
	for _, k := range keys {
		if v, ok := lookupEnv(k); ok {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", h[:8])
}

func lookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}
