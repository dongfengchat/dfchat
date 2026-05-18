package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GeminiProvider wires Google's Generative Language API. Unlike
// Claude/OpenAI which accept image URLs, Gemini insists on inline
// base64 — so this provider does the extra fetch+encode itself.
// Slightly more bandwidth but Gemini Flash is the cheapest of the
// three so it pays back fast.
type GeminiProvider struct {
	apiKey string
	model  string
	hc     *http.Client
}

func NewGeminiProvider(apiKey, model string) *GeminiProvider {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	return &GeminiProvider{
		apiKey: apiKey,
		model:  model,
		hc:     &http.Client{Timeout: 30 * time.Second}, // +10s for the image fetch
	}
}

func (g *GeminiProvider) Name() string { return "gemini:" + g.model }

func (g *GeminiProvider) ModerateImage(ctx context.Context, imageURL string) (*Verdict, error) {
	if g.apiKey == "" {
		return nil, ErrMissingAPIKey
	}
	// 1. Fetch the image so we can base64 it inline.
	imgReq, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, err
	}
	imgRes, err := g.hc.Do(imgReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: fetch image %s: %w", imageURL, err)
	}
	defer imgRes.Body.Close()
	if imgRes.StatusCode != 200 {
		return nil, fmt.Errorf("gemini: image fetch status=%d", imgRes.StatusCode)
	}
	imgBytes, err := io.ReadAll(imgRes.Body)
	if err != nil {
		return nil, err
	}
	mime := imgRes.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/jpeg" // safe default for the thumbs we serve
	}

	// 2. Build Gemini request.
	body := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{"text": PromptZH},
					map[string]any{
						"inline_data": map[string]any{
							"mime_type": mime,
							"data":      base64.StdEncoding.EncodeToString(imgBytes),
						},
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     0.0,
			"maxOutputTokens": 256,
		},
	}
	buf, _ := json.Marshal(body)
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", g.model, g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	res, err := g.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("gemini: status=%d body=%s", res.StatusCode, string(raw))
	}
	var rsp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w (raw=%s)", err, string(raw))
	}
	if len(rsp.Candidates) == 0 || len(rsp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini: empty candidates (raw=%s)", string(raw))
	}
	return parseVerdictJSON(rsp.Candidates[0].Content.Parts[0].Text, g.Name())
}
