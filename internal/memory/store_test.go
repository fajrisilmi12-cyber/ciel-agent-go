package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fajrisilmi12-cyber/hermes-agent-go/internal/state"
)

func TestStoreTruncatesAndBuildsContext(t *testing.T) {
	db, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewStore(db, 8)
	if err := store.Set(context.Background(), TargetUser, "12345678é"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(context.Background(), TargetUser)
	if err != nil {
		t.Fatal(err)
	}
	if value != "12345678" {
		t.Fatalf("value = %q, want truncated value", value)
	}
	if err := store.Set(context.Background(), TargetMemory, "notes"); err != nil {
		t.Fatal(err)
	}
	contextText, err := store.Context(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contextText, "User profile:") || !strings.Contains(contextText, "Persistent notes:") {
		t.Fatalf("context = %q", contextText)
	}
}
