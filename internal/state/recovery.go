package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// InterruptedTurn represents a partially-completed agent turn.
type InterruptedTurn struct {
	ID               int64
	SessionID        string
	Iteration        int
	MessagesSnapshot string // JSON-encoded []providers.Message
	ToolState        string // JSON-encoded map
	CreatedAt        time.Time
}

// RecoveryStore manages interrupted-turn checkpoints.
type RecoveryStore struct {
	db         *sql.DB
	maxRetries int
}

// NewRecoveryStore returns a store with the given retry budget.
func NewRecoveryStore(db *sql.DB, maxRetries int) *RecoveryStore {
	if maxRetries <= 0 {
		maxRetries = 2
	}
	return &RecoveryStore{db: db, maxRetries: maxRetries}
}

// SaveCheckpoint persists the current turn state for crash recovery.
func (s *RecoveryStore) SaveCheckpoint(ctx context.Context, sessionID string, iteration int, messagesJSON, toolStateJSON string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO interrupted_turns (session_id, iteration, messages_snapshot, tool_state, created_at) VALUES (?, ?, ?, ?, ?)`,
		sessionID, iteration, messagesJSON, toolStateJSON, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return nil
}

// SaveCheckpointMessages is a convenience that marshals Go values.
func (s *RecoveryStore) SaveCheckpointMessages(ctx context.Context, sessionID string, iteration int, messages any, toolState map[string]any) error {
	msgJSON, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("marshal messages: %w", err)
	}
	stateJSON, err := json.Marshal(toolState)
	if err != nil {
		return fmt.Errorf("marshal tool state: %w", err)
	}
	return s.SaveCheckpoint(ctx, sessionID, iteration, string(msgJSON), string(stateJSON))
}

// FindInterrupted returns all checkpoints for a session (newest first).
func (s *RecoveryStore) FindInterrupted(ctx context.Context, sessionID string) ([]InterruptedTurn, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, iteration, messages_snapshot, tool_state, created_at
		 FROM interrupted_turns WHERE session_id = ? ORDER BY id DESC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("find interrupted: %w", err)
	}
	defer rows.Close()
	var turns []InterruptedTurn
	for rows.Next() {
		var t InterruptedTurn
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Iteration, &t.MessagesSnapshot, &t.ToolState, &t.CreatedAt); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	return turns, rows.Err()
}

// MarkRecovered deletes a checkpoint after successful recovery.
func (s *RecoveryStore) MarkRecovered(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM interrupted_turns WHERE id = ?`, id)
	return err
}

// CleanStale removes checkpoints older than 24 hours.
func (s *RecoveryStore) CleanStale(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `DELETE FROM interrupted_turns WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MaxRetries returns the configured retry budget.
func (s *RecoveryStore) MaxRetries() int { return s.maxRetries }
