package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestZernioPostDefaultsToPreview(t *testing.T) {
	arguments, _ := json.Marshal(map[string]any{"caption": "hello", "media_url": "https://cdn.example/image.jpg"})
	result, err := (ZernioPoster{}).Execute(context.Background(), arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "publish preview only") {
		t.Fatalf("result = %q", result)
	}
}

func TestZernioPostSendsApprovedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/posts" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("unexpected request: %s %s", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"post-123","status":"published"}`))
	}))
	defer server.Close()
	arguments, _ := json.Marshal(map[string]any{"caption": "hello", "media_url": "https://cdn.example/image.jpg", "confirm": true})
	result, err := (ZernioPoster{APIKey: "test-key", BaseURL: server.URL, AllowPublish: true, HTTPClient: server.Client()}).Execute(context.Background(), arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "post-123") {
		t.Fatalf("result = %q", result)
	}
}
