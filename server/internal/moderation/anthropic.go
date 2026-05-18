package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AnthropicProvider wires Claude's image-input chat endpoint. Model
// defaults to claude-sonnet-4-7 (good vision + cheaper than Opus).
// API key + model both configurable so the operator can A/B
// Sonnet vs Haiku vs Opus without code change.
type AnthropicProvider struct {
	apiKey string
	model  string
	hc     *http.Client
}

func NewAnthropicProvider(apiKey, model string) *AnthropicProvider {
	if model == "" {
		model = "claude-sonnet-4-7"
	}
	return &AnthropicProvider{
		apiKey: apiKey,
		model:  model,
		// Image moderation should be fast — bound the call to 20 s so
		// a stalled API doesn't park the worker tick.
		hc: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *AnthropicProvider) Name() string { return "anthropic:" + a.model }

func (a *AnthropicProvider) ModerateImage(ctx context.Context, imageURL string) (*Verdict, error) {
	if a.apiKey == "" {
		return nil, ErrMissingAPIKey
	}
	// Claude's vision API accepts both URL refs and base64. URL is
	// simpler — server02's thumbnail is public.
	body := map[string]any{
		"model":      a.model,
		"max_tokens": 256,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type": "url",
							"url":  imageURL,
						},
					},
					map[string]any{
						"type": "text",
						"text": PromptZH,
					},
				},
			},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	res, err := a.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("anthropic: status=%d body=%s", res.StatusCode, string(raw))
	}
	// Response shape: { content: [ { type:"text", text:"..." } ] }
	var rsp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w (raw=%s)", err, string(raw))
	}
	if len(rsp.Content) == 0 {
		return nil, fmt.Errorf("anthropic: empty content (raw=%s)", string(raw))
	}
	return parseVerdictJSON(rsp.Content[0].Text, a.Name())
}
