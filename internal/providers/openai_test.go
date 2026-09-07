package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		var payload CompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "test-model" || len(payload.Messages) != 1 {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}data: [DONE]`))
	}))
	defer server.Close()

	provider := &OpenAICompatible{BaseURL: server.URL + "/v1", Model: "test-model", HTTPClient: server.Client()}
	response, err := provider.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Content != "hello" {
		t.Fatalf("content = %q, want hello", response.Message.Content)
	}
}
