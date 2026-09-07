package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFilesFindsTextAndSkipsDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc hello() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "pkg", "ignored.js"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	arguments, _ := json.Marshal(map[string]string{"query": "hello"})
	result, err := (searchFilesTool{root: root}).Execute(context.Background(), arguments)
	if err != nil {
		t.Fatal(err)
	}
	if result != "main.go" {
		t.Fatalf("result = %q, want main.go", result)
	}
}

func TestSearchFilesRejectsEscapingPath(t *testing.T) {
	arguments, _ := json.Marshal(map[string]string{"query": "hello", "path": ".."})
	_, err := (searchFilesTool{root: t.TempDir()}).Execute(context.Background(), arguments)
	if err == nil || !strings.Contains(err.Error(), "escapes working directory") {
		t.Fatalf("error = %v, want path escape error", err)
	}
}
