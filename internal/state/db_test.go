package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	var tableName string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&tableName); err != nil {
		t.Fatalf("sessions table was not created: %v", err)
	}
}
