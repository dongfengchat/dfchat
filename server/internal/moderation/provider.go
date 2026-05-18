// Package moderation defines a model-agnostic interface for AI-driven
// live-content review and ships three concrete providers — Anthropic
// Claude, OpenAI GPT, Google Gemini — plus an OpenAI-API-compatible
// "local" provider so the operator can plug in Ollama, vLLM, 通义千问,
// 智谱 GLM, DeepSeek, or any other self-hosted model that exposes the
// OpenAI vision schema.
//
// The package contains zero business logic about reports / database;
// it ONLY accepts an image URL and returns a Verdict. The worker in
// server/internal/live/moderation_worker.go (or main.go's loop) is
// responsible for translating a flagged Verdict into a row in
// live_room_reports with reporter_id = NULL.
//
// Switching providers is a config change, not a code change. Future
// additions (e.g. Baidu, Tencent CMS) drop in as new files under
// this package implementing Provider.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Category is the enum of violations we ask each model to score. The
// names match what viewer-side reportLiveRoom accepts so a flagged
// AI Verdict translates into the same `reason` column the human
// reports use — one review queue, two producers.
type Category string

const (
	CategoryNSFW     Category = "nsfw"
	CategoryViolence Category = "violence"
	CategoryPolitics Category = "politics"
	CategoryGambling Category = "gambling"
	CategoryFraud    Category = "fraud"
)

// AllCategories is the canonical ordering used in prompts so different
// models return consistent shapes for ensemble / debugging.
var AllCategories = []Category{
	CategoryNSFW, CategoryViolence, CategoryPolitics, CategoryGambling, CategoryFraud,
}

// Verdict is the moderation result for one frame. Categories maps
// each enum to a 0..1 confidence; Flagged is computed by the worker
// against the configured threshold, not the provider. Reason is
// short free-form text the model returned (in Chinese, prompted) —
// surfaces in the admin queue as the AI's stated justification.
type Verdict struct {
	Categories map[Category]float64 `json:"categories"`
	Reason     string               `json:"reason"`
	Provider   string               `json:"provider"`
	// MaxCategory + MaxScore are convenience accessors the worker
	// uses to pick the report's `reason` enum on a flag — picked
	// from the highest-scoring category.
	MaxCategory Category `json:"maxCategory"`
	MaxScore    float64  `json:"maxScore"`
}

// Provider is the contract every model integration implements. Just
// "look at this image and give me category scores". Stateless beyond
// connection / API key; safe to share across the worker's goroutines.
type Provider interface {
	Name() string
	ModerateImage(ctx context.Context, imageURL string) (*Verdict, error)
}

// Errors callers can match on.
var (
	ErrNoProviders   = errors.New("moderation: no providers configured")
	ErrAllFailed     = errors.New("moderation: every provider failed")
	ErrUnknownKind   = errors.New("moderation: unknown provider kind")
	ErrMissingAPIKey = errors.New("moderation: provider API key not set")
)

// PromptZH is the shared user-facing instruction every provider sends
// to its model. Identical wording across vendors so a swap doesn't
// shift the calibration meaningfully — bug fixes / category tweaks
// happen here once.
const PromptZH = `你是直播平台内容安全审核员。请审查这张直播画面，针对以下违规类别给出 0.0 到 1.0 的置信度评分（0=完全没有，1=非常确定）：

- nsfw: 色情、低俗、性暗示、裸露
- violence: 暴力、血腥、虐待、自残
- politics: 国家相关政治敏感、违禁政治内容
- gambling: 赌博、彩票、棋牌下注
- fraud: 诈骗、虚假广告、违规导流

只输出一行 JSON，结构如下，不要额外解释：
{"nsfw":0.0,"violence":0.0,"politics":0.0,"gambling":0.0,"fraud":0.0,"reason":"<一句简短中文，说明你看到了什么>"}`

// parseVerdictJSON is the shared JSON-line extractor. All three big
// providers (Claude / GPT / Gemini) sometimes wrap the JSON in code
// fences or trailing commentary even when told not to — strip both.
// Returns a populated Verdict (provider name still empty; caller
// fills) or an error.
func parseVerdictJSON(raw, providerName string) (*Verdict, error) {
	// 1. Strip ``` fences if present.
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	// 2. Find the first { and the matching last } — covers models
	//    that prepend a "Sure, here's the JSON:" preamble.
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i < 0 || j < 0 || j <= i {
		return nil, fmt.Errorf("%s: no JSON object found in response: %q", providerName, raw)
	}
	body := s[i : j+1]

	var parsed struct {
		NSFW     float64 `json:"nsfw"`
		Violence float64 `json:"violence"`
		Politics float64 `json:"politics"`
		Gambling float64 `json:"gambling"`
		Fraud    float64 `json:"fraud"`
		Reason   string  `json:"reason"`
	}
	if err := jsonUnmarshal([]byte(body), &parsed); err != nil {
		return nil, fmt.Errorf("%s: parse JSON %q: %w", providerName, body, err)
	}

	v := &Verdict{
		Provider: providerName,
		Reason:   parsed.Reason,
		Categories: map[Category]float64{
			CategoryNSFW:     clamp01(parsed.NSFW),
			CategoryViolence: clamp01(parsed.Violence),
			CategoryPolitics: clamp01(parsed.Politics),
			CategoryGambling: clamp01(parsed.Gambling),
			CategoryFraud:    clamp01(parsed.Fraud),
		},
	}
	for cat, score := range v.Categories {
		if score > v.MaxScore {
			v.MaxScore = score
			v.MaxCategory = cat
		}
	}
	return v, nil
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
