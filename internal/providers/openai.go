package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type OpenAICompatible struct {
	APIKey     string
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type completionEnvelope struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

func (p *OpenAICompatible) Complete(ctx context.Context, request CompletionRequest) (CompletionResponse, error) {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if strings.TrimSpace(request.Model) == "" {
		request.Model = p.Model
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResponse{}, fmt.Errorf("provider model is not configured")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("encode completion request: %w", err)
	}
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("create completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("send completion request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("read completion response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CompletionResponse{}, fmt.Errorf("provider returned HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var envelope completionEnvelope
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&envelope); err != nil {
		return CompletionResponse{}, fmt.Errorf("decode completion response: %w", err)
	}
	if len(envelope.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("provider returned no choices")
	}
	return CompletionResponse{Message: envelope.Choices[0].Message}, nil
}
