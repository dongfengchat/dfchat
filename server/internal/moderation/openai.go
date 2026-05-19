package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIProvider wires the OpenAI chat completions API with image
// inputs. ALSO doubles as the "local OpenAI-compatible" provider —
// just pass a different baseURL (Ollama, vLLM, LM Studio, DeepSeek,
// 通义千问, 智谱 GLM, 月之暗面, 任何兼容 /v1/chat/completions 的服务).
// Set apiKey to an empty string if your local endpoint doesn't need
// auth (most don't); we'll send the Authorization header only when
// the key is non-empty.
//
// inlineImages: when true, fetch the image bytes ourselves and embed
// as a base64 data URL. Needed because LM Studio (and at least some
// Ollama vision models) reject http(s):// URLs in the image_url
// field and only accept "data:image/...;base64,..." inline. OpenAI's
// hosted API accepts both, so the default is false to save bandwidth.
type OpenAIProvider struct {
	apiKey       string
	model        string
	baseURL      string // defaults to OpenAI's; override for local / proxy.
	inlineImages bool
	maxTokens    int
	hc           *http.Client
}

func NewOpenAIProvider(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = "gpt-4o-mini"
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	// Some users will paste "https://host/v1" as the baseURL because
	// that's the path their OpenAI-compat doc shows. Strip the
	// trailing /v1 so we don't end up POSTing to /v1/v1/chat/completions.
	baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	return &OpenAIProvider{
		apiKey:    apiKey,
		model:     model,
		baseURL:   baseURL,
		maxTokens: 256,
		hc:        &http.Client{Timeout: 60 * time.Second},
	}
}

// WithInlineImages flips the provider into base64-data-URL mode for
// LM Studio / Ollama / other local servers that don't fetch URLs.
func (o *OpenAIProvider) WithInlineImages(b bool) *OpenAIProvider {
	o.inlineImages = b
	return o
}

// WithMaxTokens raises the cap on the model's response length.
// Reasoning-mode models (Gemma/DeepSeek/etc. that emit a "thinking"
// preamble before the actual JSON) need 1024+ to fit thoughts + answer.
func (o *OpenAIProvider) WithMaxTokens(n int) *OpenAIProvider {
	if n > 0 {
		o.maxTokens = n
	}
	return o
}

// fetchAsDataURL pulls the image and returns "data:<mime>;base64,...".
// Bounded by the provider's HTTP client timeout.
func (o *OpenAIProvider) fetchAsDataURL(ctx context.Context, imageURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return "", err
	}
	res, err := o.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch image %s: %w", imageURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("fetch image: status=%d", res.StatusCode)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	mime := res.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (o *OpenAIProvider) Name() string { return "openai:" + o.model }

func (o *OpenAIProvider) ModerateImage(ctx context.Context, imageURL string) (*Verdict, error) {
	// LM Studio + most local servers reject URL refs in image_url.
	// Fetch + base64-encode to a data URL transparently.
	url := imageURL
	if o.inlineImages {
		dataURL, err := o.fetchAsDataURL(ctx, imageURL)
		if err != nil {
			return nil, err
		}
		url = dataURL
	}
	body := map[string]any{
		"model":      o.model,
		"max_tokens": o.maxTokens,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": PromptZH},
					map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": url},
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
