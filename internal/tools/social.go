package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type ZernioPoster struct {
	APIKey       string
	BaseURL      string
	AccountID    string
	AllowPublish bool
	HTTPClient   *http.Client
}

type zernioPostInput struct {
	Caption     string `json:"caption"`
	MediaURL    string `json:"media_url"`
	MediaType   string `json:"media_type"`
	Platform    string `json:"platform"`
	ContentType string `json:"content_type"`
	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone"`
	Confirm     bool   `json:"confirm"`
}

func (poster ZernioPoster) Name() string { return "zernio_post" }

func (poster ZernioPoster) Description() string {
	return "Preview or publish a public media URL through Zernio. Publishing requires explicit confirmation and HERMES_ALLOW_SOCIAL_PUBLISH=true."
}

func (poster ZernioPoster) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"caption":      map[string]any{"type": "string"},
			"media_url":    map[string]any{"type": "string"},
			"media_type":   map[string]any{"type": "string", "enum": []string{"image", "video"}},
			"platform":     map[string]any{"type": "string", "description": "Zernio platform, for example instagram or threads."},
			"content_type": map[string]any{"type": "string", "enum": []string{"post", "story", "reel"}},
			"schedule":     map[string]any{"type": "string", "description": "Optional ISO-8601 publish time."},
			"timezone":     map[string]any{"type": "string"},
			"confirm":      map[string]any{"type": "boolean", "description": "Must be true for a real publish."},
		},
		"required": []string{"caption", "media_url"},
	}
}

func (poster ZernioPoster) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var input zernioPostInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid zernio_post arguments: %w", err)
	}
	if strings.TrimSpace(input.MediaURL) == "" || strings.TrimSpace(input.Caption) == "" {
		return "", fmt.Errorf("caption and media_url are required")
	}
	if input.MediaType == "" {
		input.MediaType = "image"
	}
	if input.Platform == "" {
		input.Platform = "instagram"
	}
	if input.ContentType == "" {
		input.ContentType = "post"
	}
	if input.Timezone == "" {
		input.Timezone = "Asia/Jakarta"
	}
	if !input.Confirm || !poster.AllowPublish {
		return fmt.Sprintf("publish preview only: platform=%s content_type=%s media=%s schedule=%s. Set confirm=true and HERMES_ALLOW_SOCIAL_PUBLISH=true to publish.", input.Platform, input.ContentType, input.MediaURL, input.Schedule), nil
	}
	if strings.TrimSpace(poster.APIKey) == "" {
		return "", fmt.Errorf("Zernio API key is not configured")
	}
	baseURL := strings.TrimRight(poster.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.zernio.com"
	}
	platform := map[string]any{"platform": input.Platform}
	if poster.AccountID != "" {
		platform["accountId"] = poster.AccountID
	}
	platformData := map[string]any{}
	if input.ContentType != "post" {
		platformData["contentType"] = input.ContentType
	}
	payload := map[string]any{
		"content":    input.Caption,
		"mediaItems": []map[string]string{{"type": input.MediaType, "url": input.MediaURL}},
		"platforms":  []map[string]any{{"platform": platform["platform"], "accountId": platform["accountId"], "platformSpecificData": platformData}},
		"publishNow": input.Schedule == "",
	}
	if input.Schedule != "" {
		payload["scheduledFor"] = input.Schedule
		payload["timezone"] = input.Timezone
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Zernio payload: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, baseURL+"/v1/posts", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create Zernio request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+poster.APIKey)
	request.Header.Set("Content-Type", "application/json")
	client := poster.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("send Zernio request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return "", fmt.Errorf("read Zernio response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Zernio returned HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return fmt.Sprintf("Zernio accepted publish request: %s", strings.TrimSpace(string(body))), nil
}

func NewZernioPosterFromEnv() ZernioPoster {
	return ZernioPoster{
		APIKey:       os.Getenv("ZERNIO_API_KEY"),
		BaseURL:      os.Getenv("ZERNIO_BASE_URL"),
		AccountID:    os.Getenv("ZERNIO_IG_ACCOUNT_ID"),
		AllowPublish: strings.EqualFold(os.Getenv("HERMES_ALLOW_SOCIAL_PUBLISH"), "true"),
	}
}
