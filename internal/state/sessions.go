package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/providers"
)

type Session struct {
	ID        string
	Profile   string
	UpdatedAt string
}

func CreateSession(ctx context.Context, db *sql.DB, profile, model string) (string, error) {
	var rawID [16]byte
	if _, err := rand.Read(rawID[:]); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	id := hex.EncodeToString(rawID[:])
	_, err := db.ExecContext(ctx, `
		INSERT INTO sessions (id, profile, model, updated_at)
		VALUES (?, ?, ?, ?)
	`, id, profile, model, time.Now().UTC())
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return id, nil
}

func AddMessage(ctx context.Context, db *sql.DB, sessionID string, message providers.Message) error {
	toolCalls, err := encodeToolCalls(message.ToolCalls)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, name)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, message.Role, message.Content, toolCalls, message.ToolCallID, message.Name)
	if err != nil {
		return fmt.Errorf("add message: %w", err)
	}
	_, err = db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now().UTC(), sessionID)
	if err != nil {
		return fmt.Errorf("update session timestamp: %w", err)
	}
	return nil
}

func GetMessages(ctx context.Context, db *sql.DB, sessionID string, limit int) ([]providers.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT role, content, tool_calls, tool_call_id, name
		FROM messages
		WHERE session_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var reversed []providers.Message
	for rows.Next() {
		var message providers.Message
		var content, toolCalls, toolCallID, name sql.NullString
		if err := rows.Scan(&message.Role, &content, &toolCalls, &toolCallID, &name); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		message.Content = content.String
		message.ToolCallID = toolCallID.String
		message.Name = name.String
		if toolCalls.Valid && toolCalls.String != "" {
			if err := decodeToolCalls(toolCalls.String, &message.ToolCalls); err != nil {
				return nil, err
			}
		}
		reversed = append(reversed, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	messages := make([]providers.Message, len(reversed))
	for index := range reversed {
		messages[len(reversed)-index-1] = reversed[index]
	}
	return messages, nil
}

func ListSessions(ctx context.Context, db *sql.DB) ([]Session, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, profile, updated_at
		FROM sessions
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var session Session
		if err := rows.Scan(&session.ID, &session.Profile, &session.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

func LatestSession(ctx context.Context, db *sql.DB) (Session, error) {
	var session Session
	err := db.QueryRowContext(ctx, `
		SELECT id, profile, updated_at
		FROM sessions
		ORDER BY updated_at DESC
		LIMIT 1
	`).Scan(&session.ID, &session.Profile, &session.UpdatedAt)
	if err != nil {
		return Session{}, fmt.Errorf("get latest session: %w", err)
	}
	return session, nil
}

func FindLatestSessionByProfile(ctx context.Context, db *sql.DB, profile string) (Session, error) {
	var session Session
	err := db.QueryRowContext(ctx, `
		SELECT id, profile, updated_at
		FROM sessions
		WHERE profile = ?
		ORDER BY updated_at DESC
		LIMIT 1
	`, profile).Scan(&session.ID, &session.Profile, &session.UpdatedAt)
	if err != nil {
		return Session{}, fmt.Errorf("get session for profile %q: %w", profile, err)
	}
	return session, nil
}
