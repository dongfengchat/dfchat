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

// OpenAIProvider wires the OpenAI chat completions API with image
// inputs. ALSO doubles as the "local OpenAI-compatible" provider —
// just pass a different baseURL (Ollama, vLLM, DeepSeek, 通义千问,
// 智谱 GLM, 月之暗面, 任何兼容 /v1/chat/completions 的服务都行).
// Set apiKey to an empty string if your local endpoint doesn't need
// auth (most don't); we'll send the Authorization header only when
// the key is non-empty.
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string // defaults to OpenAI's; override for local / proxy.
	hc      *http.Client
}

func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o-mini"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (o *OpenAIProvider) Name() string { return "openai:" + o.model }

func (o *OpenAIProvider) ModerateImage(ctx context.Context, imageURL string) (*Verdict, error) {
	body := map[string]any{
		"model":      o.model,
		"max_tokens": 256,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": PromptZH},
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": imageURL},
					},
				},
			},
		},
		// Some compatible servers (vLLM) want this to enable vision.
		"temperature": 0.0,
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	res, err := o.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("openai: status=%d body=%s", res.StatusCode, string(raw))
	}
	var rsp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w (raw=%s)", err, string(raw))
	}
	if len(rsp.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty choices (raw=%s)", string(raw))
	}
	return parseVerdictJSON(rsp.Choices[0].Message.Content, o.Name())
}
