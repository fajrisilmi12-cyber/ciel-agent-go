package memory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	TargetUser   = "user"
	TargetMemory = "memory"
)

type Store struct {
	db     *sql.DB
	budget int
}

func NewStore(db *sql.DB, budget int) *Store {
	if budget <= 0 {
		budget = 6000
	}
	return &Store{db: db, budget: budget}
}

func (s *Store) Get(ctx context.Context, target string) (string, error) {
	if !validTarget(target) {
		return "", fmt.Errorf("invalid memory target %q", target)
	}
	var content string
	err := s.db.QueryRowContext(ctx, `SELECT content FROM memory WHERE target = ?`, target).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get memory: %w", err)
	}
	return content, nil
}

func (s *Store) Set(ctx context.Context, target, content string) error {
	if !validTarget(target) {
		return fmt.Errorf("invalid memory target %q", target)
	}
	content = strings.TrimSpace(content)
	contentRunes := []rune(content)
	if len(contentRunes) > s.budget {
		content = string(contentRunes[:s.budget])
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory (target, content, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(target) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at
	`, target, content, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("set memory: %w", err)
	}
	return nil
}

func (s *Store) Context(ctx context.Context) (string, error) {
	user, err := s.Get(ctx, TargetUser)
	if err != nil {
		return "", err
	}
	notes, err := s.Get(ctx, TargetMemory)
	if err != nil {
		return "", err
	}
	var sections []string
	if user != "" {
		sections = append(sections, "User profile:\n"+user)
	}
	if notes != "" {
		sections = append(sections, "Persistent notes:\n"+notes)
	}
	return strings.Join(sections, "\n\n"), nil
}

func validTarget(target string) bool {
	return target == TargetUser || target == TargetMemory
}
